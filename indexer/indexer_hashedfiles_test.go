package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yoanbernabeu/grepai/store"
)

func fileSHA256(tb testing.TB, path string) string {
	tb.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("failed to read %s: %v", path, err)
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// TestIndexAll_PopulatesHashedFilesForUnchangedFiles ensures the decision
// phase keeps FileInfo for files it had to hash, even when re-indexing is
// skipped. Startup symbol extraction reuses this map to avoid a second read.
func TestIndexAll_PopulatesHashedFilesForUnchangedFiles(t *testing.T) {
	tmpDir := t.TempDir()
	const fileCount = 3
	createGoFixtureFiles(t, tmpDir, fileCount)

	ignoreMatcher, err := NewIgnoreMatcher(tmpDir, []string{}, "")
	if err != nil {
		t.Fatalf("failed to create ignore matcher: %v", err)
	}

	mockStore := newMockStore()
	scanner := NewScanner(tmpDir, ignoreMatcher)
	// Seed documents with the real content hashes so the hash gate skips embed.
	for i := 0; i < fileCount; i++ {
		rel := fmt.Sprintf("file_%04d.go", i)
		hash := fileSHA256(t, filepath.Join(tmpDir, rel))
		mockStore.documents[rel] = store.Document{
			Path:     rel,
			Hash:     hash,
			ChunkIDs: []string{"c1"},
		}
	}

	mockEmbedder := newMockEmbedder()
	chunker := NewChunker(512, 50)
	// Zero lastIndexTime forces the mtime gate off so every file is hashed.
	idx := NewIndexer(tmpDir, mockStore, mockEmbedder, chunker, scanner, time.Time{})

	stats, err := idx.IndexAllWithProgress(context.Background(), nil)
	if err != nil {
		t.Fatalf("IndexAllWithProgress failed: %v", err)
	}

	if stats.FilesIndexed != 0 {
		t.Fatalf("expected 0 indexed files, got %d", stats.FilesIndexed)
	}
	if mockEmbedder.embedCalled {
		t.Fatal("embedder should not be called when content hashes match")
	}
	if len(stats.HashedFiles) != fileCount {
		t.Fatalf("expected HashedFiles to contain %d entries, got %d", fileCount, len(stats.HashedFiles))
	}
	for i := 0; i < fileCount; i++ {
		rel := fmt.Sprintf("file_%04d.go", i)
		fi, ok := stats.HashedFiles[rel]
		if !ok {
			t.Fatalf("expected HashedFiles to contain %s", rel)
		}
		if fi.Hash == "" || fi.Content == "" {
			t.Fatalf("expected HashedFiles[%s] to include content and hash", rel)
		}
		if fi.Hash != fileSHA256(t, filepath.Join(tmpDir, rel)) {
			t.Fatalf("HashedFiles[%s] hash mismatch", rel)
		}
	}
}

// TestIndexAll_HashedFilesEmptyWhenMtimeGateSkips ensures the healthy
// restart path (mtime gate hits) does not read files, so HashedFiles stays empty.
func TestIndexAll_HashedFilesEmptyWhenMtimeGateSkips(t *testing.T) {
	tmpDir := t.TempDir()
	const fileCount = 5
	createGoFixtureFiles(t, tmpDir, fileCount)

	ignoreMatcher, err := NewIgnoreMatcher(tmpDir, []string{}, "")
	if err != nil {
		t.Fatalf("failed to create ignore matcher: %v", err)
	}

	mockStore := newMockStore()
	for i := 0; i < fileCount; i++ {
		rel := fmt.Sprintf("file_%04d.go", i)
		mockStore.documents[rel] = store.Document{
			Path:     rel,
			Hash:     "seeded",
			ChunkIDs: []string{"c1"},
		}
	}

	mockEmbedder := newMockEmbedder()
	scanner := NewScanner(tmpDir, ignoreMatcher)
	chunker := NewChunker(512, 50)
	lastIndexTime := time.Now().Add(1 * time.Hour)
	idx := NewIndexer(tmpDir, mockStore, mockEmbedder, chunker, scanner, lastIndexTime)

	stats, err := idx.IndexAllWithProgress(context.Background(), nil)
	if err != nil {
		t.Fatalf("IndexAllWithProgress failed: %v", err)
	}

	if stats.FilesIndexed != 0 {
		t.Fatalf("expected 0 indexed files, got %d", stats.FilesIndexed)
	}
	if len(stats.HashedFiles) != 0 {
		t.Fatalf("expected empty HashedFiles when mtime gate skips, got %d", len(stats.HashedFiles))
	}
	if scanner.ScanFileCallCount() != 0 {
		t.Fatalf("expected no ScanFile calls on mtime-gate path, got %d", scanner.ScanFileCallCount())
	}
}
