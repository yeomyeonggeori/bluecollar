package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	codexEndpointName       = "codex"
	defaultCodexModelName   = "gpt-6.2-sol"
	codexOAuthClientID      = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexOAuthAuthorizeURL  = "https://auth.openai.com/oauth/authorize"
	codexOAuthTokenURL      = "https://auth.openai.com/oauth/token"
	codexOAuthCallbackAddr  = "127.0.0.1:1455"
	codexOAuthRedirectURI   = "http://localhost:1455/auth/callback"
	codexOAuthOriginator    = "codex_cli_rs"
	codexClientVersion      = "0.169.0"
	codexOAuthScopes        = "openid profile email offline_access"
	codexTokenRefreshMargin = 2 * time.Minute
	codexOAuthLoginTimeout  = 3 * time.Minute
)

type codexCredentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
	sourcePath   string
}

func isCodexEndpoint(endpointURL string) bool {
	normalized := strings.ToLower(strings.TrimSpace(endpointURL))
	return normalized == codexEndpointName || normalized == "chatgpt" || normalized == "openai-codex"
}

func bluecollarCodexAuthPath() string {
	if configPath := configFilePath(); configPath != "" {
		return filepath.Join(filepath.Dir(configPath), "codex-auth.json")
	}
	return ""
}

func defaultCodexCLIAuthPath() string {
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		return filepath.Join(codexHome, "auth.json")
	}
	home, errorValue := os.UserHomeDir()
	if errorValue != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "auth.json")
}

func loadCodexCredentials() (codexCredentials, error) {
	candidates := []string{
		bluecollarCodexAuthPath(),
		defaultCodexCLIAuthPath(),
	}
	for _, path := range candidates {
		if strings.TrimSpace(path) == "" {
			continue
		}
		credentials, errorValue := loadCodexCredentialsFromFile(path)
		if errorValue == nil && strings.TrimSpace(credentials.AccessToken) != "" {
			return credentials, nil
		}
	}
	return codexCredentials{}, errors.New("not signed in to ChatGPT/Codex; run /login codex or bluecollar --login codex")
}

func loadCodexCredentialsFromFile(path string) (codexCredentials, error) {
	data, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return codexCredentials{}, errorValue
	}
	return parseCodexAuthJSON(data, path)
}

func parseCodexAuthJSON(data []byte, sourcePath string) (codexCredentials, error) {
	var envelope struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
		ExpiresAt    int64  `json:"expires_at"`
		Tokens       *struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			AccountID    string `json:"account_id"`
			IDToken      string `json:"id_token"`
		} `json:"tokens"`
	}
	if errorValue := json.Unmarshal(data, &envelope); errorValue != nil {
		return codexCredentials{}, errorValue
	}
	credentials := codexCredentials{
		AccessToken:  strings.TrimSpace(envelope.AccessToken),
		RefreshToken: strings.TrimSpace(envelope.RefreshToken),
		AccountID:    strings.TrimSpace(envelope.AccountID),
		ExpiresAt:    envelope.ExpiresAt,
		sourcePath:   sourcePath,
	}
	if envelope.Tokens != nil {
		credentials.AccessToken = firstNonEmpty(credentials.AccessToken, envelope.Tokens.AccessToken)
		credentials.RefreshToken = firstNonEmpty(credentials.RefreshToken, envelope.Tokens.RefreshToken)
		credentials.AccountID = firstNonEmpty(credentials.AccountID, envelope.Tokens.AccountID)
	}
	if credentials.AccessToken == "" {
		return codexCredentials{}, errors.New("codex auth file contains no access_token")
	}
	if credentials.AccountID == "" {
		credentials.AccountID = extractChatGPTAccountIDFromJWT(credentials.AccessToken)
	}
	if credentials.ExpiresAt == 0 {
		credentials.ExpiresAt = extractJWTExpiry(credentials.AccessToken)
	}
	return credentials, nil
}

func saveCodexCredentials(path string, credentials codexCredentials) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if errorValue := os.MkdirAll(filepath.Dir(path), 0o700); errorValue != nil {
		return errorValue
	}
	document, errorValue := json.MarshalIndent(credentials, "", "  ")
	if errorValue != nil {
		return errorValue
	}
	return os.WriteFile(path, append(document, '\n'), 0o600)
}

func (credentials codexCredentials) isExpiringSoon(now time.Time) bool {
	if credentials.ExpiresAt <= 0 {
		return false
	}
	return now.Add(codexTokenRefreshMargin).Unix() >= credentials.ExpiresAt
}

func extractChatGPTAccountIDFromJWT(token string) string {
	claims := decodeJWTPayload(token)
	if claims == nil {
		return ""
	}
	authClaim, isMap := claims["https://api.openai.com/auth"].(map[string]any)
	if !isMap {
		return ""
	}
	accountID, _ := authClaim["chatgpt_account_id"].(string)
	return strings.TrimSpace(accountID)
}

func extractJWTExpiry(token string) int64 {
	claims := decodeJWTPayload(token)
	if claims == nil {
		return 0
	}
	exp, isFloat := claims["exp"].(float64)
	if !isFloat {
		return 0
	}
	return int64(exp)
}

func decodeJWTPayload(token string) map[string]any {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return nil
	}
	decoded, errorValue := base64.RawURLEncoding.DecodeString(parts[1])
	if errorValue != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(decoded, &claims) != nil {
		return nil
	}
	return claims
}

func refreshCodexCredentials(ctx context.Context, httpClient *http.Client, tokenURL string, credentials codexCredentials) (codexCredentials, error) {
	if strings.TrimSpace(credentials.RefreshToken) == "" {
		return codexCredentials{}, errors.New("codex access token expired and no refresh token is available; run /login codex")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {credentials.RefreshToken},
		"client_id":     {codexOAuthClientID},
	}
	refreshed, errorValue := postCodexTokenRequest(ctx, httpClient, tokenURL, form)
	if errorValue != nil {
		return codexCredentials{}, errorValue
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = credentials.RefreshToken
	}
	if refreshed.AccountID == "" {
		refreshed.AccountID = credentials.AccountID
	}
	refreshed.sourcePath = firstNonEmpty(bluecollarCodexAuthPath(), credentials.sourcePath)
	_ = saveCodexCredentials(refreshed.sourcePath, refreshed)
	return refreshed, nil
}

func exchangeCodexAuthorizationCode(ctx context.Context, httpClient *http.Client, tokenURL string, code string, verifier string) (codexCredentials, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {codexOAuthClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {codexOAuthRedirectURI},
	}
	return postCodexTokenRequest(ctx, httpClient, tokenURL, form)
}

func postCodexTokenRequest(ctx context.Context, httpClient *http.Client, tokenURL string, form url.Values) (codexCredentials, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	request, errorValue := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if errorValue != nil {
		return codexCredentials{}, errorValue
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, errorValue := httpClient.Do(request)
	if errorValue != nil {
		return codexCredentials{}, errorValue
	}
	defer response.Body.Close()
	body, errorValue := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if errorValue != nil {
		return codexCredentials{}, errorValue
	}
	if response.StatusCode != http.StatusOK {
		return codexCredentials{}, fmt.Errorf("oauth token request returned %d: %s", response.StatusCode, collapsedWhitespace(string(body)))
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if errorValue := json.Unmarshal(body, &payload); errorValue != nil {
		return codexCredentials{}, errorValue
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return codexCredentials{}, errors.New("oauth token response omitted access_token")
	}
	expiresAt := extractJWTExpiry(payload.AccessToken)
	if expiresAt == 0 && payload.ExpiresIn > 0 {
		expiresAt = time.Now().Unix() + payload.ExpiresIn
	}
	return codexCredentials{
		AccessToken:  strings.TrimSpace(payload.AccessToken),
		RefreshToken: strings.TrimSpace(payload.RefreshToken),
		AccountID:    extractChatGPTAccountIDFromJWT(payload.AccessToken),
		ExpiresAt:    expiresAt,
	}, nil
}

func generatePKCEPair() (string, string, string, error) {
	verifierBytes := make([]byte, 32)
	if _, errorValue := rand.Read(verifierBytes); errorValue != nil {
		return "", "", "", errorValue
	}
	stateBytes := make([]byte, 16)
	if _, errorValue := rand.Read(stateBytes); errorValue != nil {
		return "", "", "", errorValue
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state := base64.RawURLEncoding.EncodeToString(stateBytes)
	return verifier, challenge, state, nil
}

func buildCodexAuthorizeURL(challenge string, state string) string {
	query := url.Values{
		"response_type":              {"code"},
		"client_id":                  {codexOAuthClientID},
		"redirect_uri":               {codexOAuthRedirectURI},
		"scope":                      {codexOAuthScopes},
		"code_challenge":             {challenge},
		"code_challenge_method":      {"S256"},
		"state":                      {state},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {codexOAuthOriginator},
	}
	return codexOAuthAuthorizeURL + "?" + query.Encode()
}

func runCodexOAuthLogin(ctx context.Context, onAuthURL func(string)) (codexCredentials, error) {
	verifier, challenge, expectedState, errorValue := generatePKCEPair()
	if errorValue != nil {
		return codexCredentials{}, errorValue
	}
	listener, listenError := net.Listen("tcp", codexOAuthCallbackAddr)
	if listenError != nil {
		return codexCredentials{}, fmt.Errorf("could not bind OAuth callback %s: %w", codexOAuthCallbackAddr, listenError)
	}
	defer listener.Close()

	codeChannel := make(chan string, 1)
	errorChannel := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(writer http.ResponseWriter, request *http.Request) {
		if oauthError := request.URL.Query().Get("error"); oauthError != "" {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(writer, "Authentication failed: "+oauthError)
			select {
			case errorChannel <- errors.New("oauth error: " + oauthError):
			default:
			}
			return
		}
		if request.URL.Query().Get("state") != expectedState {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(writer, "Invalid OAuth state.")
			return
		}
		code := strings.TrimSpace(request.URL.Query().Get("code"))
		if code == "" {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(writer, "Missing authorization code.")
			return
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(writer, "<!doctype html><html><body><h2>Signed in to bluecollar.</h2><p>You can close this window and return to the terminal.</p></body></html>")
		select {
		case codeChannel <- code:
		default:
		}
	})
	server := &http.Server{Handler: mux}
	go func() {
		_ = server.Serve(listener)
	}()
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()

	authorizeURL := buildCodexAuthorizeURL(challenge, expectedState)
	if onAuthURL != nil {
		onAuthURL(authorizeURL)
	}
	openBrowserURL(authorizeURL)

	loginContext, cancelLogin := context.WithTimeout(ctx, codexOAuthLoginTimeout)
	defer cancelLogin()
	var code string
	select {
	case code = <-codeChannel:
	case callbackError := <-errorChannel:
		return codexCredentials{}, callbackError
	case <-loginContext.Done():
		if errors.Is(ctx.Err(), context.Canceled) {
			return codexCredentials{}, context.Canceled
		}
		return codexCredentials{}, errors.New("timed out waiting for ChatGPT OAuth callback")
	}

	credentials, exchangeError := exchangeCodexAuthorizationCode(loginContext, http.DefaultClient, codexOAuthTokenURL, code, verifier)
	if exchangeError != nil {
		return codexCredentials{}, exchangeError
	}
	savePath := bluecollarCodexAuthPath()
	credentials.sourcePath = savePath
	if saveError := saveCodexCredentials(savePath, credentials); saveError != nil {
		return codexCredentials{}, saveError
	}
	return credentials, nil
}

func openBrowserURL(targetURL string) {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", targetURL)
	case "linux":
		command = exec.Command("xdg-open", targetURL)
	default:
		return
	}
	_ = command.Start()
}
