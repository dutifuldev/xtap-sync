package syncer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncDeduplicatesAndPreservesTweetFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "source")
	repo := filepath.Join(root, "repo")
	mustMkdir(t, source)
	mustMkdir(t, repo)
	initRepo(t, repo)

	writeFile(t, filepath.Join(source, "tweets-2026-06-17.jsonl"), strings.Join([]string{
		`{"id":"1","captured_at":"2026-06-17T10:00:00.000Z","text":"old"}`,
		`{"id":"2","captured_at":"2026-06-17T11:00:00.000Z","text":"two"}`,
		"",
	}, "\n"))
	writeFile(t, filepath.Join(source, "tweets-2026-06-18.jsonl"), strings.Join([]string{
		`{"id":"1","captured_at":"2026-06-18T10:00:00.000Z","text":"new"}`,
		"",
	}, "\n"))
	writeFile(t, filepath.Join(source, "media", "ignored.jpg"), "not json")

	result, err := Sync(context.Background(), SyncOptions{
		SourceDir:     source,
		RepoDir:       repo,
		Remote:        "",
		CommitMessage: "sync test",
		Push:          false,
	})
	if err != nil {
		t.Fatal(err)
	}

	if result.WrittenRecords != 2 {
		t.Fatalf("WrittenRecords = %d, want 2", result.WrittenRecords)
	}
	if result.WrittenFiles != 2 {
		t.Fatalf("WrittenFiles = %d, want 2", result.WrittenFiles)
	}

	day17 := readFile(t, filepath.Join(repo, repoTweetPath("2026-06-17")))
	if strings.Contains(day17, `"id":"1"`) {
		t.Fatalf("older duplicate remained in day17: %s", day17)
	}
	if !strings.Contains(day17, `"id":"2"`) {
		t.Fatalf("day17 missing id 2: %s", day17)
	}

	day18 := readFile(t, filepath.Join(repo, repoTweetPath("2026-06-18")))
	if !strings.Contains(day18, `"id":"1"`) || !strings.Contains(day18, `"new"`) {
		t.Fatalf("day18 missing newer duplicate: %s", day18)
	}

	if _, err := os.Stat(filepath.Join(repo, "tweets-2026-06-18.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("tweet files should not be written at repo root, err=%v", err)
	}

	if _, err := os.Stat(filepath.Join(repo, "media", "ignored.jpg")); !os.IsNotExist(err) {
		t.Fatalf("media file should not be copied, err=%v", err)
	}
}

func TestVerifyReportsMissingTweets(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "source")
	repo := filepath.Join(root, "repo")
	mustMkdir(t, source)
	mustMkdir(t, repo)

	writeFile(t, filepath.Join(source, "tweets-2026-06-17.jsonl"), `{"id":"1","captured_at":"2026-06-17T10:00:00.000Z"}`+"\n")
	writeFile(t, filepath.Join(repo, repoTweetPath("2026-06-17")), `{"id":"2","captured_at":"2026-06-17T10:00:00.000Z"}`+"\n")

	report, err := Verify(source, repo)
	if err != nil {
		t.Fatal(err)
	}

	if report.SourceUnique != 1 || report.RepoUnique != 1 {
		t.Fatalf("unexpected counts: %+v", report)
	}
	if len(report.MissingIDs) != 1 || report.MissingIDs[0] != "1" {
		t.Fatalf("MissingIDs = %#v, want [1]", report.MissingIDs)
	}
	if len(report.ExtraIDs) != 1 || report.ExtraIDs[0] != "2" {
		t.Fatalf("ExtraIDs = %#v, want [2]", report.ExtraIDs)
	}
}

func TestRemoteRecordsAreMerged(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	repo := filepath.Join(root, "repo")
	source := filepath.Join(root, "source")
	mustMkdir(t, source)

	run(t, "", "git", "init", "--bare", "-b", "main", remote)
	run(t, "", "git", "clone", remote, work)
	configGitUser(t, work)
	writeFile(t, filepath.Join(work, "tweets-2026-06-16.jsonl"), `{"id":"remote","captured_at":"2026-06-16T10:00:00.000Z"}`+"\n")
	run(t, work, "git", "add", "tweets-2026-06-16.jsonl")
	run(t, work, "git", "commit", "-m", "seed")
	run(t, work, "git", "push", "origin", "main")

	run(t, "", "git", "clone", remote, repo)
	configGitUser(t, repo)
	writeFile(t, filepath.Join(source, "tweets-2026-06-17.jsonl"), `{"id":"local","captured_at":"2026-06-17T10:00:00.000Z"}`+"\n")

	_, err := Sync(context.Background(), SyncOptions{
		SourceDir:     source,
		RepoDir:       repo,
		CommitMessage: "sync remote",
		Push:          false,
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(repo, repoTweetPath("2026-06-16"))); !strings.Contains(got, `"id":"remote"`) {
		t.Fatalf("missing remote record: %s", got)
	}
	if got := readFile(t, filepath.Join(repo, repoTweetPath("2026-06-17"))); !strings.Contains(got, `"id":"local"`) {
		t.Fatalf("missing local record: %s", got)
	}
}

func TestMalformedRemoteJSONLReturnsError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	repo := filepath.Join(root, "repo")
	source := filepath.Join(root, "source")
	mustMkdir(t, source)

	run(t, "", "git", "init", "--bare", "-b", "main", remote)
	run(t, "", "git", "clone", remote, work)
	configGitUser(t, work)
	writeFile(t, filepath.Join(work, "tweets-2026-06-16.jsonl"), "{not-json}\n")
	run(t, work, "git", "add", "tweets-2026-06-16.jsonl")
	run(t, work, "git", "commit", "-m", "seed bad data")
	run(t, work, "git", "push", "origin", "main")

	run(t, "", "git", "clone", remote, repo)
	configGitUser(t, repo)
	if err := os.Remove(filepath.Join(repo, "tweets-2026-06-16.jsonl")); err != nil {
		t.Fatal(err)
	}

	_, err := Sync(context.Background(), SyncOptions{
		SourceDir: source,
		RepoDir:   repo,
		Remote:    "origin",
		Push:      false,
	})
	if err == nil || !strings.Contains(err.Error(), "remote tweets-2026-06-16.jsonl") {
		t.Fatalf("err = %v, want malformed remote JSON error", err)
	}
}

func TestSyncInitializesEmptyRepositoryWithoutCommit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "source")
	repo := filepath.Join(root, "repo")
	mustMkdir(t, source)
	mustMkdir(t, repo)

	result, err := Sync(context.Background(), SyncOptions{
		SourceDir: source,
		RepoDir:   repo,
		Remote:    "",
		Push:      false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Committed {
		t.Fatal("empty sync should not commit")
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Fatalf("repo was not initialized: %v", err)
	}
}

func TestSyncSkipsPushWhenRemoteMissing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "source")
	repo := filepath.Join(root, "repo")
	mustMkdir(t, source)
	mustMkdir(t, repo)
	initRepo(t, repo)
	writeFile(t, filepath.Join(source, "tweets-2026-06-18.jsonl"), `{"id":"1","captured_at":"2026-06-18T10:00:00.000Z"}`+"\n")

	result, err := Sync(context.Background(), SyncOptions{
		SourceDir: source,
		RepoDir:   repo,
		Remote:    "missing",
		Push:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pushed {
		t.Fatal("sync should not push when remote is missing")
	}
}

func TestSyncPushesWhenRemoteExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	repo := filepath.Join(root, "repo")
	source := filepath.Join(root, "source")
	mustMkdir(t, source)

	run(t, "", "git", "init", "--bare", "-b", "main", remote)
	run(t, "", "git", "clone", remote, repo)
	configGitUser(t, repo)
	writeFile(t, filepath.Join(source, "tweets-2026-06-18.jsonl"), `{"id":"pushed","captured_at":"2026-06-18T10:00:00.000Z"}`+"\n")

	result, err := Sync(context.Background(), SyncOptions{
		SourceDir:     source,
		RepoDir:       repo,
		CommitMessage: "sync push",
		Push:          true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Pushed {
		t.Fatalf("Pushed = false, want true")
	}

	out := gitOut(t, repo, "ls-remote", "--heads", "origin", "main")
	if !strings.Contains(out, "refs/heads/main") {
		t.Fatalf("remote main not pushed: %s", out)
	}
}

func TestPushIfEnabledReturnsRetryError(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	initRepo(t, repo)
	run(t, repo, "git", "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))

	pushed, err := pushIfEnabled(context.Background(), SyncOptions{
		RepoDir: repo,
		Remote:  "origin",
		Push:    true,
	}, "main")
	if err == nil {
		t.Fatal("expected push retry error")
	}
	if pushed {
		t.Fatal("push should report false after retry failure")
	}
}

func TestRetryAfterRejectedPushReappliesOnRemoteHistory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	repo := filepath.Join(root, "repo")
	source := filepath.Join(root, "source")
	mustMkdir(t, source)

	run(t, "", "git", "init", "--bare", "-b", "main", remote)
	run(t, "", "git", "clone", remote, first)
	configGitUser(t, first)
	writeFile(t, filepath.Join(first, "tweets-2026-06-16.jsonl"), `{"id":"base","captured_at":"2026-06-16T10:00:00.000Z"}`+"\n")
	run(t, first, "git", "add", "tweets-2026-06-16.jsonl")
	run(t, first, "git", "commit", "-m", "seed")
	run(t, first, "git", "push", "origin", "main")

	run(t, "", "git", "clone", remote, repo)
	configGitUser(t, repo)

	run(t, "", "git", "clone", remote, second)
	configGitUser(t, second)
	writeFile(t, filepath.Join(second, "tweets-2026-06-17.jsonl"), `{"id":"remote-new","captured_at":"2026-06-17T10:00:00.000Z"}`+"\n")
	writeFile(t, filepath.Join(second, "README.md"), "remote readme\n")
	run(t, second, "git", "add", "tweets-2026-06-17.jsonl")
	run(t, second, "git", "add", "README.md")
	run(t, second, "git", "commit", "-m", "remote advance")
	run(t, second, "git", "push", "origin", "main")

	writeFile(t, filepath.Join(source, "tweets-2026-06-18.jsonl"), `{"id":"local-new","captured_at":"2026-06-18T10:00:00.000Z"}`+"\n")
	if err := retryAfterRejectedPush(context.Background(), SyncOptions{
		SourceDir:     source,
		RepoDir:       repo,
		Remote:        "origin",
		CommitMessage: "retry sync",
		Push:          true,
	}, "main"); err != nil {
		t.Fatal(err)
	}

	run(t, repo, "git", "fetch", "origin", "main")
	if !strings.Contains(readFile(t, filepath.Join(repo, repoTweetPath("2026-06-17"))), `"id":"remote-new"`) {
		t.Fatal("missing remote record after retry")
	}
	if !strings.Contains(readFile(t, filepath.Join(repo, repoTweetPath("2026-06-18"))), `"id":"local-new"`) {
		t.Fatal("missing local record after retry")
	}
	if got := readFile(t, filepath.Join(repo, "README.md")); got != "remote readme\n" {
		t.Fatalf("README.md = %q, want remote contents", got)
	}
	out := gitOut(t, repo, "merge-base", "--is-ancestor", "origin/main", "HEAD")
	if out != "" {
		t.Fatalf("unexpected merge-base output: %s", out)
	}
	parents := strings.Fields(gitOut(t, repo, "rev-list", "--max-count=1", "--parents", "HEAD"))
	if len(parents) != 2 {
		t.Fatalf("HEAD has %d parents, want 1 parent: %v", len(parents)-1, parents)
	}
	mergeCount := strings.TrimSpace(gitOut(t, repo, "rev-list", "--merges", "--count", "origin/main..HEAD"))
	if mergeCount != "0" {
		t.Fatalf("merge commits after origin/main = %s, want 0", mergeCount)
	}
}

func TestRetryAfterRejectedPushRefusesNonManagedLocalCommit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	repo := filepath.Join(root, "repo")
	source := filepath.Join(root, "source")
	mustMkdir(t, source)

	run(t, "", "git", "init", "--bare", "-b", "main", remote)
	run(t, "", "git", "clone", remote, first)
	configGitUser(t, first)
	writeFile(t, filepath.Join(first, "README.md"), "base\n")
	run(t, first, "git", "add", "README.md")
	run(t, first, "git", "commit", "-m", "seed")
	run(t, first, "git", "push", "origin", "main")

	run(t, "", "git", "clone", remote, repo)
	configGitUser(t, repo)
	writeFile(t, filepath.Join(repo, "README.md"), "local unpushed\n")
	run(t, repo, "git", "add", "README.md")
	run(t, repo, "git", "commit", "-m", "local readme")

	run(t, "", "git", "clone", remote, second)
	configGitUser(t, second)
	writeFile(t, filepath.Join(second, "tweets-2026-06-17.jsonl"), `{"id":"remote-new","captured_at":"2026-06-17T10:00:00.000Z"}`+"\n")
	run(t, second, "git", "add", "tweets-2026-06-17.jsonl")
	run(t, second, "git", "commit", "-m", "remote advance")
	run(t, second, "git", "push", "origin", "main")

	writeFile(t, filepath.Join(source, "tweets-2026-06-18.jsonl"), `{"id":"local-new","captured_at":"2026-06-18T10:00:00.000Z"}`+"\n")
	err := retryAfterRejectedPush(context.Background(), SyncOptions{
		SourceDir:     source,
		RepoDir:       repo,
		Remote:        "origin",
		CommitMessage: "retry sync",
		Push:          true,
	}, "main")
	if err == nil {
		t.Fatal("expected retry to reject non-managed local commit")
	}
	if !strings.Contains(err.Error(), "non-managed path") {
		t.Fatalf("retry error = %v, want non-managed path", err)
	}
	if got := readFile(t, filepath.Join(repo, "README.md")); got != "local unpushed\n" {
		t.Fatalf("README.md = %q, want local unpushed contents", got)
	}
}

func TestEnsureAheadChangesRejectsCommittedMixedRename(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	repo := filepath.Join(root, "repo")
	work := filepath.Join(root, "work")

	run(t, "", "git", "init", "--bare", "-b", "main", remote)
	run(t, "", "git", "clone", remote, work)
	configGitUser(t, work)
	writeFile(t, filepath.Join(work, "README.md"), strings.Repeat("same line\n", 40))
	run(t, work, "git", "add", "README.md")
	run(t, work, "git", "commit", "-m", "seed")
	run(t, work, "git", "push", "origin", "main")

	run(t, "", "git", "clone", remote, repo)
	configGitUser(t, repo)
	run(t, repo, "git", "config", "diff.renames", "true")

	renamed := filepath.Join(repo, repoTweetPath("2026-06-18"))
	mustMkdir(t, filepath.Dir(renamed))
	if err := os.Rename(filepath.Join(repo, "README.md"), renamed); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", "-A")
	run(t, repo, "git", "commit", "-m", "rename readme")

	err := ensureAheadChangesAreManagedTweets(context.Background(), repo, "origin/main")
	if err == nil {
		t.Fatal("expected committed mixed rename to block linear retry")
	}
	if !strings.Contains(err.Error(), "README.md") {
		t.Fatalf("retry error = %v, want README.md", err)
	}
}

func TestEnsureLinearRetrySafeRejectsIgnoredRemoteObstacle(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	repo := filepath.Join(root, "repo")

	run(t, "", "git", "init", "--bare", "-b", "main", remote)
	run(t, "", "git", "clone", remote, first)
	configGitUser(t, first)
	writeFile(t, filepath.Join(first, ".gitignore"), "*.log\n")
	run(t, first, "git", "add", ".gitignore")
	run(t, first, "git", "commit", "-m", "ignore logs")
	run(t, first, "git", "push", "origin", "main")

	run(t, "", "git", "clone", remote, repo)
	configGitUser(t, repo)
	writeFile(t, filepath.Join(repo, "sync.log"), "local ignored\n")
	writeFile(t, filepath.Join(repo, "local-only.log"), "harmless ignored\n")

	run(t, "", "git", "clone", remote, second)
	configGitUser(t, second)
	writeFile(t, filepath.Join(second, "sync.log"), "remote tracked\n")
	run(t, second, "git", "add", "-f", "sync.log")
	run(t, second, "git", "commit", "-m", "track sync log")
	run(t, second, "git", "push", "origin", "main")
	run(t, repo, "git", "fetch", "origin", "main")

	err := ensureLinearRetrySafe(context.Background(), repo, "origin/main")
	if err == nil {
		t.Fatal("expected ignored remote obstacle to block linear retry")
	}
	if !strings.Contains(err.Error(), "sync.log") {
		t.Fatalf("retry error = %v, want sync.log", err)
	}
}

func TestEnsureLinearRetrySafeRejectsIgnoredPathUnderRemoteFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	repo := filepath.Join(root, "repo")

	run(t, "", "git", "init", "--bare", "-b", "main", remote)
	run(t, "", "git", "clone", remote, first)
	configGitUser(t, first)
	writeFile(t, filepath.Join(first, ".gitignore"), "cache/\n")
	run(t, first, "git", "add", ".gitignore")
	run(t, first, "git", "commit", "-m", "ignore cache")
	run(t, first, "git", "push", "origin", "main")

	run(t, "", "git", "clone", remote, repo)
	configGitUser(t, repo)
	writeFile(t, filepath.Join(repo, "cache", "tmp.log"), "local ignored\n")

	run(t, "", "git", "clone", remote, second)
	configGitUser(t, second)
	writeFile(t, filepath.Join(second, "cache"), "remote tracked file\n")
	run(t, second, "git", "add", "-f", "cache")
	run(t, second, "git", "commit", "-m", "track cache file")
	run(t, second, "git", "push", "origin", "main")
	run(t, repo, "git", "fetch", "origin", "main")

	err := ensureLinearRetrySafe(context.Background(), repo, "origin/main")
	if err == nil {
		t.Fatal("expected ignored path below remote file to block linear retry")
	}
	if !strings.Contains(err.Error(), "cache") {
		t.Fatalf("retry error = %v, want cache", err)
	}
}

func TestEnsureNoDirtyNonManagedPaths(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, filepath.Join(repo, "README.md"), "dirty\n")
	if err := ensureNoDirtyNonManagedPaths(context.Background(), repo); err == nil {
		t.Fatal("expected dirty README.md to block linear retry")
	}

	run(t, repo, "git", "add", "README.md")
	run(t, repo, "git", "commit", "-m", "readme")
	writeFile(t, filepath.Join(repo, repoTweetPath("2026-06-18")), `{"id":"tweet","captured_at":"2026-06-18T10:00:00.000Z"}`+"\n")
	run(t, repo, "git", "add", repoTweetPath("2026-06-18"))
	run(t, repo, "git", "commit", "-m", "tweet")
	writeFile(t, filepath.Join(repo, repoTweetPath("2026-06-18")), `{"id":"tweet","captured_at":"2026-06-18T11:00:00.000Z"}`+"\n")
	if err := ensureNoDirtyNonManagedPaths(context.Background(), repo); err != nil {
		t.Fatalf("dirty managed tweet path should be allowed: %v", err)
	}
}

func TestEnsureNoDirtyNonManagedPathsRejectsMixedRename(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, filepath.Join(repo, "README.md"), "source\n")
	run(t, repo, "git", "add", "README.md")
	run(t, repo, "git", "commit", "-m", "readme")

	renamed := filepath.Join(repo, repoTweetPath("2026-06-18"))
	mustMkdir(t, filepath.Dir(renamed))
	if err := os.Rename(filepath.Join(repo, "README.md"), renamed); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", "-A")

	err := ensureNoDirtyNonManagedPaths(context.Background(), repo)
	if err == nil {
		t.Fatal("expected mixed rename to block linear retry")
	}
	if !strings.Contains(err.Error(), "README.md") {
		t.Fatalf("retry error = %v, want README.md", err)
	}
}

func TestFetchRemoteRefReturnsFetchedBranch(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	repo := filepath.Join(root, "repo")
	work := filepath.Join(root, "work")
	run(t, "", "git", "init", "--bare", "-b", "main", remote)
	run(t, "", "git", "clone", remote, work)
	configGitUser(t, work)
	writeFile(t, filepath.Join(work, "README.md"), "seed\n")
	run(t, work, "git", "add", "README.md")
	run(t, work, "git", "commit", "-m", "seed")
	run(t, work, "git", "push", "origin", "main")
	run(t, "", "git", "clone", remote, repo)

	ref := fetchRemoteRef(context.Background(), SyncOptions{RepoDir: repo, Remote: "origin"}, "main")
	if ref != "origin/main" {
		t.Fatalf("remote ref = %q, want origin/main", ref)
	}
}

func TestReadRemoteTweetFilesReadsStructuredPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	repo := filepath.Join(root, "repo")
	run(t, "", "git", "init", "--bare", "-b", "main", remote)
	run(t, "", "git", "clone", remote, repo)
	configGitUser(t, repo)
	writeFile(t, filepath.Join(repo, repoTweetPath("2026-06-18")), `{"id":"structured","captured_at":"2026-06-18T10:00:00.000Z"}`+"\n")
	run(t, repo, "git", "add", repoTweetPath("2026-06-18"))
	run(t, repo, "git", "commit", "-m", "seed structured")
	run(t, repo, "git", "push", "origin", "main")

	seq := 0
	records, err := readRemoteTweetFiles(context.Background(), repo, "origin/main", &seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != "structured" || records[0].FileDate != "2026-06-18" {
		t.Fatalf("records = %+v, want structured remote tweet", records)
	}
}

func TestMalformedJSONLReturnsError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "source")
	repo := filepath.Join(root, "repo")
	mustMkdir(t, source)
	mustMkdir(t, repo)
	initRepo(t, repo)
	writeFile(t, filepath.Join(source, "tweets-2026-06-18.jsonl"), "{not-json}\n")

	_, err := Sync(context.Background(), SyncOptions{
		SourceDir: source,
		RepoDir:   repo,
		Remote:    "",
		Push:      false,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("err = %v, want invalid JSON error", err)
	}
}

func TestCommitManagedChangesPropagatesGitErrors(t *testing.T) {
	t.Parallel()

	_, err := commitManagedChanges(context.Background(), filepath.Join(t.TempDir(), "missing"), "commit")
	if err == nil {
		t.Fatal("expected addManagedPaths error for missing repo")
	}

	_, err = commitManagedChanges(context.Background(), t.TempDir(), "commit")
	if err == nil {
		t.Fatal("expected staged diff error")
	}
}

func TestManagedGitPathsIncludeTrackedDeletedTweets(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, filepath.Join(repo, "tweets-2026-06-18.jsonl"), `{"id":"1"}`+"\n")
	run(t, repo, "git", "add", "tweets-2026-06-18.jsonl")
	run(t, repo, "git", "commit", "-m", "seed")
	if err := os.Remove(filepath.Join(repo, "tweets-2026-06-18.jsonl")); err != nil {
		t.Fatal(err)
	}

	paths, err := managedGitPaths(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "tweets-2026-06-18.jsonl" {
		t.Fatalf("paths = %#v, want deleted legacy tweet file", paths)
	}
}

func TestArchiveWalkErrorsAreReturned(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	archiveRoot := filepath.Join(root, tweetArchiveDir)
	blocked := filepath.Join(archiveRoot, "blocked")
	mustMkdir(t, blocked)
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(blocked, 0o755)
	})

	if _, err := archiveTweetFilePaths(archiveRoot); err == nil {
		t.Fatal("expected archive path walk error")
	}
	if _, err := archiveDirs(root); err == nil {
		t.Fatal("expected archive dir walk error")
	}
}

func TestSyncRejectsMissingRequiredOptions(t *testing.T) {
	t.Parallel()

	_, err := Sync(context.Background(), SyncOptions{})
	if err == nil || !strings.Contains(err.Error(), "source") {
		t.Fatalf("err = %v, want source error", err)
	}

	_, err = Sync(context.Background(), SyncOptions{SourceDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "repo") {
		t.Fatalf("err = %v, want repo error", err)
	}
}

func TestInstallLaunchdServiceRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	if _, err := InstallLaunchdService(ServiceConfig{}); err == nil {
		t.Fatal("expected invalid service config error")
	}
}

func TestUninstallLaunchdServiceRejectsMissingLabel(t *testing.T) {
	t.Parallel()

	if _, err := UninstallLaunchdService(""); err == nil {
		t.Fatal("expected missing label error")
	}
}

func TestMergeHelpers(t *testing.T) {
	t.Parallel()

	if got := sortKey(tweetRecord{ID: "id", CapturedAt: "2026-06-18T10:00:00.000Z"}); got != "2026-06-18T10:00:00.000Z" {
		t.Fatalf("sortKey captured_at = %q", got)
	}
	if got := sortKey(tweetRecord{ID: "id-only"}); got != "id-only" {
		t.Fatalf("sortKey = %q, want id-only", got)
	}
	if got := sortKey(tweetRecord{ID: "id", CreatedAt: "2026-06-18T09:00:00.000Z"}); got != "2026-06-18T09:00:00.000Z" {
		t.Fatalf("sortKey created_at = %q", got)
	}

	records := map[string]tweetRecord{}
	addRecords(records, []tweetRecord{
		{ID: "", Priority: 99},
		{ID: "same", CapturedAt: "2026-06-18T09:00:00.000Z", CreatedAt: "2026-06-18T09:00:00.000Z", Priority: 1, Seq: 1},
		{ID: "same", CapturedAt: "2026-06-18T10:00:00.000Z", CreatedAt: "2026-06-18T09:00:00.000Z", Priority: 1, Seq: 2},
		{ID: "same", CapturedAt: "2026-06-18T08:00:00.000Z", CreatedAt: "2026-06-18T09:00:00.000Z", Priority: 2, Seq: 3},
	})
	if records["same"].Priority != 2 {
		t.Fatalf("priority did not win: %+v", records["same"])
	}

	if !betterRecord(
		tweetRecord{CreatedAt: "2026-06-18T11:00:00.000Z", Priority: 1},
		tweetRecord{CreatedAt: "2026-06-18T10:00:00.000Z", Priority: 1},
	) {
		t.Fatal("newer created_at should win")
	}
	if !betterRecord(tweetRecord{Seq: 2, Priority: 1}, tweetRecord{Seq: 1, Priority: 1}) {
		t.Fatal("later sequence should win")
	}
}

func TestLowLevelHelpers(t *testing.T) {
	t.Parallel()

	if got := canonicalJSON("{not-json}"); got != "{not-json}" {
		t.Fatalf("canonicalJSON invalid = %q", got)
	}
	if _, err := ParseInterval("not-a-duration"); err == nil {
		t.Fatal("expected ParseInterval error")
	}

	repo := t.TempDir()
	if err := addManagedPaths(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, repoTweetPath("2026-06-18")), `{"id":"stale"}`+"\n")
	if err := removeManagedTweetFiles(repo, map[string][]tweetRecord{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, repoTweetPath("2026-06-18"))); !os.IsNotExist(err) {
		t.Fatalf("stale tweet file should be removed, err=%v", err)
	}

	initRepo(t, repo)
	branch, err := currentBranch(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "main" {
		t.Fatalf("branch = %q, want main", branch)
	}

	writeFile(t, filepath.Join(repo, "README.md"), "test\n")
	run(t, repo, "git", "add", "README.md")
	run(t, repo, "git", "commit", "-m", "seed")
	run(t, repo, "git", "checkout", "--detach", "HEAD")
	branch, err = currentBranch(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "main" {
		t.Fatalf("detached branch fallback = %q, want main", branch)
	}
}

func TestStructuredArchiveHelpers(t *testing.T) {
	t.Parallel()

	if got := repoTweetPath("2026-06-18"); got != "data/tweets/2026/06/tweets-2026-06-18.jsonl" {
		t.Fatalf("repoTweetPath = %q", got)
	}
	if got := repoTweetPath("bad-date"); got != "data/tweets/tweets-bad-date.jsonl" {
		t.Fatalf("repoTweetPath fallback = %q", got)
	}

	for _, path := range []string{
		"tweets-2026-06-18.jsonl",
		"data/tweets/2026/06/tweets-2026-06-18.jsonl",
	} {
		date, ok := managedTweetPathDate(path)
		if !ok || date != "2026-06-18" {
			t.Fatalf("managedTweetPathDate(%q) = %q, %t", path, date, ok)
		}
	}
	if _, ok := managedTweetPathDate("other/tweets-2026-06-18.jsonl"); ok {
		t.Fatal("non-archive nested tweet path should not be managed")
	}

	var records []tweetRecord
	records = append(records,
		tweetRecord{ID: "b", CapturedAt: "2026-06-18T10:00:00.000Z"},
		tweetRecord{ID: "a", CapturedAt: "2026-06-18T10:00:00.000Z"},
		tweetRecord{ID: "c", CapturedAt: "2026-06-18T09:00:00.000Z"},
	)
	sortTweetRecords(records)
	if got := records[0].ID + records[1].ID + records[2].ID; got != "cab" {
		t.Fatalf("sorted IDs = %q, want cab", got)
	}
}

func TestStructuredArchiveReadAndCleanup(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	seq := 0
	records, err := readArchiveTweetFiles(filepath.Join(repo, tweetArchiveDir), 1, &seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("missing archive records = %d, want 0", len(records))
	}

	writeFile(t, filepath.Join(repo, repoTweetPath("2026-06-18")), `{"id":"archive","captured_at":"2026-06-18T10:00:00.000Z"}`+"\n")
	writeFile(t, filepath.Join(repo, "tweets-2026-06-17.jsonl"), `{"id":"legacy","captured_at":"2026-06-17T10:00:00.000Z"}`+"\n")
	records, err = readRepoTweetFiles(repo, 1, &seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("repo records = %d, want 2", len(records))
	}

	paths, err := existingManagedTweetPaths(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"data/tweets/2026/06/tweets-2026-06-18.jsonl", "tweets-2026-06-17.jsonl"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("managed paths = %#v, want %#v", paths, want)
	}

	if err := removeManagedTweetFiles(repo, map[string][]tweetRecord{
		"2026-06-18": {tweetRecord{ID: "archive"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "tweets-2026-06-17.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("legacy root tweet should be removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, repoTweetPath("2026-06-18"))); err != nil {
		t.Fatalf("kept archive tweet missing: %v", err)
	}
}

func initRepo(t *testing.T, repo string) {
	t.Helper()
	run(t, repo, "git", "init", "-b", "main")
	configGitUser(t, repo)
}

func configGitUser(t *testing.T, repo string) {
	t.Helper()
	run(t, repo, "git", "config", "user.email", "test@example.invalid")
	run(t, repo, "git", "config", "user.name", "Test User")
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, string(out))
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}
