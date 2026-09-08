package loop

import (
	"context"
	"testing"
	"time"
)

type blockingEmbeddingProvider struct{}

func (blockingEmbeddingProvider) GenerateEmbedding(ctx context.Context, _ string) ([]float32, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestSkillSearchDegradesToBM25WhenEmbeddingBlocks(t *testing.T) {
	retriever := NewEmbeddingSkillRetriever(blockingEmbeddingProvider{}, t.TempDir())
	skillInstructions := []SkillInstruction{{Name: "calendar", Description: "calendar management", Prompt: "calendar skill"}}

	startedAt := time.Now()
	result := retriever.Search(context.Background(), AgentRequest{Prompt: "set up a meeting"}, skillInstructions, SkillSearchQuerySet{}, 3)

	if elapsed := time.Since(startedAt); elapsed > skillEmbeddingSearchTimeout+5*time.Second {
		t.Fatalf("expected the search to degrade within the timeout, took %s", elapsed)
	}
	if result.RetrievalMode != "bm25_fallback" {
		t.Fatalf("expected BM25 degradation when embedding blocks, got %+v", result)
	}
}

func TestSkillSearchDegradesToBM25WhenIndexLockIsHeld(t *testing.T) {
	skillRetriever := NewEmbeddingSkillRetriever(blockingEmbeddingProvider{}, t.TempDir())
	skillRetriever.mutex.Lock()
	defer skillRetriever.mutex.Unlock()
	searchContext, cancelSearch := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelSearch()
	startedAt := time.Now()
	result := skillRetriever.Search(searchContext, AgentRequest{Prompt: "leave a note"}, []SkillInstruction{{Name: "mattermost", Description: "message"}}, SkillSearchQuerySet{}, 3)
	if result.RetrievalMode != "bm25_fallback" {
		t.Fatalf("expected bm25 fallback while the lock is held, got %+v", result)
	}
	if time.Since(startedAt) > 5*time.Second {
		t.Fatalf("expected fast fallback, took %s", time.Since(startedAt))
	}
}

type slowEmbeddingProvider struct{ delay time.Duration }

func (provider slowEmbeddingProvider) GenerateEmbedding(ctx context.Context, _ string) ([]float32, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(provider.delay):
		return []float32{1, 0}, nil
	}
}

func TestQueryEmbeddingsAreAskedForTogether(t *testing.T) {
	retriever := NewEmbeddingSkillRetriever(slowEmbeddingProvider{delay: 200 * time.Millisecond}, t.TempDir())
	querySet := SkillSearchQuerySet{}
	for range 5 {
		querySet.Queries = append(querySet.Queries, SkillSearchQuery{Description: "calendar"})
	}

	startedAt := time.Now()
	embeddings := retriever.queryEmbeddings(context.Background(), querySet)

	if len(embeddings) != 5 {
		t.Fatalf("expected one embedding per query, got %d", len(embeddings))
	}
	if elapsed := time.Since(startedAt); elapsed > 600*time.Millisecond {
		t.Fatalf("five queries asked one after another would take a second; together they took %s", elapsed)
	}
}
