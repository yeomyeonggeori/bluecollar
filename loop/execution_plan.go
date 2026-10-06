package loop

const RequesterAuthorizationExplicit = "explicit"

const RequesterAuthorizationImplied = "implied"

const RequesterAuthorizationAbsent = "absent"

type ConfirmationReplyDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}
