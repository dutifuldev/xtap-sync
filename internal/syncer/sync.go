package syncer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const tweetArchiveDir = "data/tweets"

var tweetFileRE = regexp.MustCompile(`^tweets-(\d{4})-(\d{2})-(\d{2})\.jsonl$`)

type SyncOptions struct {
	SourceDir     string
	RepoDir       string
	Remote        string
	Branch        string
	CommitMessage string
	Push          bool
}

type SyncResult struct {
	SourceRecords  int
	RepoRecords    int
	WrittenRecords int
	WrittenFiles   int
	Committed      bool
	Pushed         bool
}

type VerifyReport struct {
	SourceUnique int
	RepoUnique   int
	MissingIDs   []string
	ExtraIDs     []string
}

type tweetRecord struct {
	ID         string
	CapturedAt string
	CreatedAt  string
	FileDate   string
	Raw        string
	Priority   int
	Seq        int
}

type mergeInput struct {
	SourceDir string
	RepoDir   string
	RemoteRef string
}

type mergeState struct {
	records map[string]tweetRecord
	seq     int
	result  SyncResult
}

func Sync(ctx context.Context, opts SyncOptions) (SyncResult, error) {
	opts, err := normalizeOptions(opts)
	if err != nil {
		return SyncResult{}, err
	}

	if err := ensureGitRepo(ctx, opts.RepoDir); err != nil {
		return SyncResult{}, err
	}

	branch, err := resolveBranch(ctx, opts)
	if err != nil {
		return SyncResult{}, err
	}
	remoteRef := fetchRemoteRef(ctx, opts, branch)

	result, err := mergeToRepo(ctx, mergeInput{
		SourceDir: opts.SourceDir,
		RepoDir:   opts.RepoDir,
		RemoteRef: remoteRef,
	})
	if err != nil {
		return SyncResult{}, err
	}

	committed, err := commitManagedChanges(ctx, opts.RepoDir, opts.CommitMessage)
	if err != nil {
		return SyncResult{}, err
	}
	result.Committed = committed

	pushed, err := pushIfEnabled(ctx, opts, branch)
	if err != nil {
		return result, err
	}
	result.Pushed = pushed

	return result, nil
}

func resolveBranch(ctx context.Context, opts SyncOptions) (string, error) {
	if opts.Branch != "" {
		return opts.Branch, nil
	}
	return currentBranch(ctx, opts.RepoDir)
}

func fetchRemoteRef(ctx context.Context, opts SyncOptions, branch string) string {
	if opts.Remote == "" || !remoteExists(ctx, opts.RepoDir, opts.Remote) {
		return ""
	}
	if err := git(ctx, opts.RepoDir, "fetch", "--prune", opts.Remote, branch); err != nil {
		return ""
	}
	return opts.Remote + "/" + branch
}

func commitManagedChanges(ctx context.Context, repoDir, message string) (bool, error) {
	if err := addManagedPaths(ctx, repoDir); err != nil {
		return false, err
	}
	changed, err := hasStagedChanges(ctx, repoDir)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	return true, git(ctx, repoDir, "commit", "-m", message)
}

func pushIfEnabled(ctx context.Context, opts SyncOptions, branch string) (bool, error) {
	if !opts.Push || opts.Remote == "" || !remoteExists(ctx, opts.RepoDir, opts.Remote) {
		return false, nil
	}
	if err := git(ctx, opts.RepoDir, "push", opts.Remote, branch); err != nil {
		if retryErr := retryAfterRejectedPush(ctx, opts, branch); retryErr != nil {
			return false, errors.Join(err, retryErr)
		}
	}
	return true, nil
}

func retryAfterRejectedPush(ctx context.Context, opts SyncOptions, branch string) error {
	if err := git(ctx, opts.RepoDir, "fetch", "--prune", opts.Remote, branch); err != nil {
		return err
	}

	remoteRef := opts.Remote + "/" + branch
	if err := ensureLinearRetrySafe(ctx, opts.RepoDir, remoteRef); err != nil {
		return err
	}

	state, err := loadMergeRecords(ctx, mergeInput{
		SourceDir: opts.SourceDir,
		RepoDir:   opts.RepoDir,
		RemoteRef: remoteRef,
	})
	if err != nil {
		return err
	}

	byDate := recordsByDate(state.records)
	if err := git(ctx, opts.RepoDir, "reset", "--hard", remoteRef); err != nil {
		return err
	}
	if err := applyRecordsToRepo(opts.RepoDir, byDate); err != nil {
		return err
	}
	if _, err := commitManagedChanges(ctx, opts.RepoDir, opts.CommitMessage); err != nil {
		return err
	}

	return git(ctx, opts.RepoDir, "push", opts.Remote, branch)
}

func ensureLinearRetrySafe(ctx context.Context, repoDir, remoteRef string) error {
	if err := ensureNoDirtyNonManagedPaths(ctx, repoDir); err != nil {
		return err
	}
	if err := ensureNoIgnoredRemoteObstacles(ctx, repoDir, remoteRef); err != nil {
		return err
	}
	return ensureAheadChangesAreManagedTweets(ctx, repoDir, remoteRef)
}

func ensureNoDirtyNonManagedPaths(ctx context.Context, repoDir string) error {
	out, err := gitOutput(ctx, repoDir, "status", "--porcelain")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		for _, path := range statusPaths(line) {
			if _, ok := managedTweetPathDate(path); !ok {
				return fmt.Errorf("retry would reset non-managed working tree path %q", path)
			}
		}
	}
	return nil
}

func ensureAheadChangesAreManagedTweets(ctx context.Context, repoDir, remoteRef string) error {
	out, err := gitOutput(ctx, repoDir, "diff", "--no-renames", "--name-only", remoteRef+"...HEAD")
	if err != nil {
		return err
	}
	for _, path := range strings.Split(strings.TrimSpace(out), "\n") {
		if path == "" {
			continue
		}
		if _, ok := managedTweetPathDate(path); !ok {
			return fmt.Errorf("retry cannot reset local commit touching non-managed path %q", path)
		}
	}
	return nil
}

func ensureNoIgnoredRemoteObstacles(ctx context.Context, repoDir, remoteRef string) error {
	ignored, err := ignoredWorktreePaths(ctx, repoDir)
	if err != nil {
		return err
	}
	if len(ignored) == 0 {
		return nil
	}

	remotePaths, err := remoteTreePaths(ctx, repoDir, remoteRef)
	if err != nil {
		return err
	}
	for _, path := range ignored {
		if remoteTracksPathOrChild(remotePaths, path) {
			return fmt.Errorf("retry would reset ignored non-managed path %q tracked by %s", path, remoteRef)
		}
	}
	return nil
}

func ignoredWorktreePaths(ctx context.Context, repoDir string) ([]string, error) {
	out, err := gitOutput(ctx, repoDir, "status", "--porcelain", "--ignored=matching", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "!! ") {
			continue
		}
		path := strings.TrimSuffix(filepath.ToSlash(line[3:]), "/")
		if _, ok := managedTweetPathDate(path); !ok {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func remoteTreePaths(ctx context.Context, repoDir, remoteRef string) ([]string, error) {
	out, err := gitOutput(ctx, repoDir, "ls-tree", "-r", "--name-only", remoteRef)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, path := range strings.Split(strings.TrimSpace(out), "\n") {
		if path != "" {
			paths = append(paths, filepath.ToSlash(path))
		}
	}
	return paths, nil
}

func remoteTracksPathOrChild(remotePaths []string, localPath string) bool {
	for _, remotePath := range remotePaths {
		if remotePath == localPath ||
			strings.HasPrefix(remotePath, localPath+"/") ||
			strings.HasPrefix(localPath, remotePath+"/") {
			return true
		}
	}
	return false
}

func statusPaths(line string) []string {
	if len(line) < 3 {
		return nil
	}
	path := line[3:]
	if isRenameOrCopyStatus(line) {
		if before, after, ok := strings.Cut(path, " -> "); ok {
			return []string{before, after}
		}
	}
	return []string{path}
}

func isRenameOrCopyStatus(line string) bool {
	return line[0] == 'R' || line[0] == 'C' || line[1] == 'R' || line[1] == 'C'
}

func normalizeOptions(opts SyncOptions) (SyncOptions, error) {
	if opts.SourceDir == "" {
		return opts, fmt.Errorf("source directory is required")
	}
	if opts.RepoDir == "" {
		return opts, fmt.Errorf("repo directory is required")
	}
	if opts.Remote == "" {
		opts.Remote = "origin"
	}
	if opts.CommitMessage == "" {
		opts.CommitMessage = "sync xtap tweets"
	}

	source, err := filepath.Abs(opts.SourceDir)
	if err != nil {
		return opts, err
	}
	repo, err := filepath.Abs(opts.RepoDir)
	if err != nil {
		return opts, err
	}

	opts.SourceDir = source
	opts.RepoDir = repo
	return opts, nil
}

func mergeToRepo(ctx context.Context, input mergeInput) (SyncResult, error) {
	state, err := loadMergeRecords(ctx, input)
	if err != nil {
		return SyncResult{}, err
	}
	byDate := recordsByDate(state.records)

	if err := applyRecordsToRepo(input.RepoDir, byDate); err != nil {
		return state.result, err
	}
	state.result.WrittenFiles = len(byDate)
	state.result.WrittenRecords = len(state.records)
	return state.result, nil
}

func applyRecordsToRepo(repoDir string, byDate map[string][]tweetRecord) error {
	if err := removeManagedTweetFiles(repoDir, byDate); err != nil {
		return err
	}

	var result SyncResult
	return writeRecordsByDate(repoDir, byDate, &result)
}

func loadMergeRecords(ctx context.Context, input mergeInput) (mergeState, error) {
	state := mergeState{records: map[string]tweetRecord{}}
	if err := state.addRepoRecords(input.RepoDir); err != nil {
		return state, err
	}
	if input.RemoteRef != "" {
		if err := state.addRemoteRecords(ctx, input.RepoDir, input.RemoteRef); err != nil {
			return state, err
		}
	}
	if err := state.addSourceRecords(input.SourceDir); err != nil {
		return state, err
	}
	return state, nil
}

func (state *mergeState) addRepoRecords(repoDir string) error {
	records, err := readRepoTweetFiles(repoDir, 1, &state.seq)
	if err != nil {
		return err
	}
	state.addExistingRecords(records)
	return nil
}

func (state *mergeState) addRemoteRecords(ctx context.Context, repoDir, remoteRef string) error {
	records, err := readRemoteTweetFiles(ctx, repoDir, remoteRef, &state.seq)
	if err != nil {
		return err
	}
	state.addExistingRecords(records)
	return nil
}

func (state *mergeState) addSourceRecords(sourceDir string) error {
	records, err := readTopLevelTweetFiles(sourceDir, 3, &state.seq)
	if err != nil {
		return err
	}
	state.result.SourceRecords = len(records)
	addRecords(state.records, records)
	return nil
}

func (state *mergeState) addExistingRecords(records []tweetRecord) {
	state.result.RepoRecords += len(records)
	addRecords(state.records, records)
}

func recordsByDate(records map[string]tweetRecord) map[string][]tweetRecord {
	byDate := map[string][]tweetRecord{}
	for _, record := range records {
		byDate[record.FileDate] = append(byDate[record.FileDate], record)
	}
	return byDate
}

func writeRecordsByDate(repoDir string, byDate map[string][]tweetRecord, result *SyncResult) error {
	for date, dateRecords := range byDate {
		sortTweetRecords(dateRecords)

		path := filepath.Join(repoDir, repoTweetPath(date))
		if err := writeJSONL(path, dateRecords); err != nil {
			return err
		}
		result.WrittenFiles++
		result.WrittenRecords += len(dateRecords)
	}
	return nil
}

func sortTweetRecords(records []tweetRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		left := sortKey(records[i])
		right := sortKey(records[j])
		if left == right {
			return records[i].ID < records[j].ID
		}
		return left < right
	})
}

func addRecords(target map[string]tweetRecord, records []tweetRecord) {
	for _, record := range records {
		if record.ID == "" {
			continue
		}
		current, ok := target[record.ID]
		if !ok || betterRecord(record, current) {
			target[record.ID] = record
		}
	}
}

func betterRecord(candidate, current tweetRecord) bool {
	if candidate.Priority != current.Priority {
		return candidate.Priority > current.Priority
	}
	if candidate.CapturedAt != current.CapturedAt {
		return candidate.CapturedAt > current.CapturedAt
	}
	if candidate.CreatedAt != current.CreatedAt {
		return candidate.CreatedAt > current.CreatedAt
	}
	return candidate.Seq > current.Seq
}

func sortKey(record tweetRecord) string {
	if record.CapturedAt != "" {
		return record.CapturedAt
	}
	if record.CreatedAt != "" {
		return record.CreatedAt
	}
	return record.ID
}

func readTopLevelTweetFiles(dir string, priority int, seq *int) ([]tweetRecord, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	var records []tweetRecord
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		date, ok := tweetFileDate(entry.Name())
		if !ok {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		fileRecords, err := readJSONLFile(path, date, priority, seq)
		if err != nil {
			return nil, err
		}
		records = append(records, fileRecords...)
	}

	return records, nil
}

func readRepoTweetFiles(repoDir string, priority int, seq *int) ([]tweetRecord, error) {
	records, err := readTopLevelTweetFiles(repoDir, priority, seq)
	if err != nil {
		return nil, err
	}

	archiveRoot := filepath.Join(repoDir, tweetArchiveDir)
	archiveRecords, err := readArchiveTweetFiles(archiveRoot, priority, seq)
	if err != nil {
		return nil, err
	}
	return append(records, archiveRecords...), nil
}

func readArchiveTweetFiles(root string, priority int, seq *int) ([]tweetRecord, error) {
	paths, err := archiveTweetFilePaths(root)
	if err != nil {
		return nil, err
	}

	var records []tweetRecord
	for _, path := range paths {
		fileRecords, err := readArchiveTweetFile(path, priority, seq)
		if err != nil {
			return nil, err
		}
		records = append(records, fileRecords...)
	}
	return records, nil
}

func archiveTweetFilePaths(root string) ([]string, error) {
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	var paths []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if _, ok := tweetFileDate(entry.Name()); ok {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	sort.Strings(paths)
	return paths, nil
}

func readArchiveTweetFile(path string, priority int, seq *int) ([]tweetRecord, error) {
	date, _ := tweetFileDate(filepath.Base(path))
	return readJSONLFile(path, date, priority, seq)
}

func readRemoteTweetFiles(ctx context.Context, repoDir, remoteRef string, seq *int) ([]tweetRecord, error) {
	out, err := gitOutput(ctx, repoDir, "ls-tree", "-r", "--name-only", remoteRef)
	if err != nil {
		return nil, err
	}

	var records []tweetRecord
	for _, name := range strings.Split(strings.TrimSpace(out), "\n") {
		if name == "" {
			continue
		}
		date, ok := managedTweetPathDate(name)
		if !ok {
			continue
		}
		content, err := gitOutput(ctx, repoDir, "show", remoteRef+":"+name)
		if err != nil {
			return nil, err
		}
		fileRecords, err := readJSONL(strings.NewReader(content), date, 2, seq)
		if err != nil {
			return nil, fmt.Errorf("remote %s: %w", name, err)
		}
		records = append(records, fileRecords...)
	}

	return records, nil
}

func readJSONLFile(path, fileDate string, priority int, seq *int) ([]tweetRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	records, err := readJSONL(file, fileDate, priority, seq)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return records, nil
}

func readJSONL(r io.Reader, fileDate string, priority int, seq *int) ([]tweetRecord, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)

	var records []tweetRecord
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var obj struct {
			ID         string `json:"id"`
			CapturedAt string `json:"captured_at"`
			CreatedAt  string `json:"created_at"`
		}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			return nil, err
		}
		if obj.ID == "" {
			continue
		}

		*seq++
		records = append(records, tweetRecord{
			ID:         obj.ID,
			CapturedAt: obj.CapturedAt,
			CreatedAt:  obj.CreatedAt,
			FileDate:   fileDate,
			Raw:        canonicalJSON(line),
			Priority:   priority,
			Seq:        *seq,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return records, nil
}

func canonicalJSON(line string) string {
	var value any
	if err := json.Unmarshal([]byte(line), &value); err != nil {
		return line
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return line
	}

	return strings.TrimSpace(buf.String())
}

func writeJSONL(path string, records []tweetRecord) error {
	var buf bytes.Buffer
	for _, record := range records {
		buf.WriteString(record.Raw)
		buf.WriteByte('\n')
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func removeManagedTweetFiles(repoDir string, byDate map[string][]tweetRecord) error {
	keep := map[string]struct{}{}
	for date := range byDate {
		keep[repoTweetPath(date)] = struct{}{}
	}

	paths, err := existingManagedTweetPaths(repoDir)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if _, ok := keep[path]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(repoDir, path)); err != nil {
			return err
		}
	}
	return pruneEmptyArchiveDirs(repoDir)
}

func existingManagedTweetPaths(repoDir string) ([]string, error) {
	paths, err := rootManagedTweetPaths(repoDir)
	if err != nil {
		return nil, err
	}
	archivePaths, err := archiveManagedTweetPaths(repoDir)
	if err != nil {
		return nil, err
	}
	paths = append(paths, archivePaths...)
	sort.Strings(paths)
	return paths, nil
}

func rootManagedTweetPaths(repoDir string) ([]string, error) {
	entries, err := os.ReadDir(repoDir)
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() {
			if _, ok := tweetFileDate(entry.Name()); ok {
				paths = append(paths, entry.Name())
			}
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func archiveManagedTweetPaths(repoDir string) ([]string, error) {
	archiveRoot := filepath.Join(repoDir, tweetArchiveDir)
	paths, err := archiveTweetFilePaths(archiveRoot)
	if err != nil {
		return nil, err
	}

	managed := make([]string, 0, len(paths))
	for _, path := range paths {
		rel, err := filepath.Rel(repoDir, path)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		if _, ok := managedTweetPathDate(rel); ok {
			managed = append(managed, rel)
		}
	}
	sort.Strings(managed)
	return managed, nil
}

func pruneEmptyArchiveDirs(repoDir string) error {
	dirs, err := archiveDirs(repoDir)
	if err != nil {
		return err
	}
	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) > len(dirs[j])
	})
	return removeEmptyDirs(dirs)
}

func archiveDirs(repoDir string) ([]string, error) {
	archiveRoot := filepath.Join(repoDir, tweetArchiveDir)
	if _, err := os.Stat(archiveRoot); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	var dirs []string
	if err := filepath.WalkDir(archiveRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return dirs, nil
}

func removeEmptyDirs(dirs []string) error {
	for _, dir := range dirs {
		if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) && !isDirectoryNotEmpty(err) {
			return err
		}
	}
	return nil
}

func isDirectoryNotEmpty(err error) bool {
	return strings.Contains(err.Error(), "directory not empty")
}

func tweetFileDate(name string) (string, bool) {
	matches := tweetFileRE.FindStringSubmatch(name)
	if len(matches) != 4 {
		return "", false
	}
	return strings.Join(matches[1:4], "-"), true
}

func managedTweetPathDate(path string) (string, bool) {
	path = filepath.ToSlash(path)
	if !strings.Contains(path, "/") {
		return tweetFileDate(path)
	}
	if !strings.HasPrefix(path, tweetArchiveDir+"/") {
		return "", false
	}
	return tweetFileDate(filepath.Base(path))
}

func repoTweetPath(date string) string {
	parts := strings.Split(date, "-")
	if len(parts) != 3 {
		return filepath.ToSlash(filepath.Join(tweetArchiveDir, fmt.Sprintf("tweets-%s.jsonl", date)))
	}
	return filepath.ToSlash(filepath.Join(tweetArchiveDir, parts[0], parts[1], fmt.Sprintf("tweets-%s.jsonl", date)))
}

func Verify(sourceDir, repoDir string) (VerifyReport, error) {
	seq := 0
	sourceRecords, err := readTopLevelTweetFiles(sourceDir, 1, &seq)
	if err != nil {
		return VerifyReport{}, err
	}
	repoRecords, err := readRepoTweetFiles(repoDir, 1, &seq)
	if err != nil {
		return VerifyReport{}, err
	}

	sourceIDs := recordIDSet(sourceRecords)
	repoIDs := recordIDSet(repoRecords)
	report := VerifyReport{
		SourceUnique: len(sourceIDs),
		RepoUnique:   len(repoIDs),
	}
	report.MissingIDs = missingIDs(sourceIDs, repoIDs)
	report.ExtraIDs = missingIDs(repoIDs, sourceIDs)
	return report, nil
}

func recordIDSet(records []tweetRecord) map[string]struct{} {
	ids := map[string]struct{}{}
	for _, record := range records {
		ids[record.ID] = struct{}{}
	}
	return ids
}

func missingIDs(want, got map[string]struct{}) []string {
	var missing []string
	for id := range want {
		if _, ok := got[id]; !ok {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	return missing
}

func ensureGitRepo(ctx context.Context, repoDir string) error {
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err == nil {
		return nil
	}
	return git(ctx, repoDir, "init", "-b", "main")
}

func currentBranch(ctx context.Context, repoDir string) (string, error) {
	out, err := gitOutput(ctx, repoDir, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(out)
	if branch == "" {
		return "main", nil
	}
	return branch, nil
}

func remoteExists(ctx context.Context, repoDir, remote string) bool {
	return git(ctx, repoDir, "remote", "get-url", remote) == nil
}

func hasStagedChanges(ctx context.Context, repoDir string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "diff", "--cached", "--quiet")
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, err
}

func addManagedPaths(ctx context.Context, repoDir string) error {
	paths, err := managedGitPaths(ctx, repoDir)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}

	args := append([]string{"add", "-A", "--"}, paths...)
	return git(ctx, repoDir, args...)
}

func managedGitPaths(ctx context.Context, repoDir string) ([]string, error) {
	seen := map[string]struct{}{}
	var paths []string
	add := func(path string) {
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	if err := addExistingRepoFiles(repoDir, add); err != nil {
		return nil, err
	}
	if err := addCurrentTweetFiles(repoDir, add); err != nil {
		return nil, err
	}
	addTrackedTweetFiles(ctx, repoDir, add)

	sort.Strings(paths)
	return paths, nil
}

func addExistingRepoFiles(repoDir string, add func(string)) error {
	for _, path := range []string{".gitignore", "README.md"} {
		if _, err := os.Stat(filepath.Join(repoDir, path)); err == nil {
			add(path)
		}
	}
	return nil
}

func addCurrentTweetFiles(repoDir string, add func(string)) error {
	paths, err := existingManagedTweetPaths(repoDir)
	if err != nil {
		return err
	}
	for _, path := range paths {
		add(path)
	}
	return nil
}

func addTrackedTweetFiles(ctx context.Context, repoDir string, add func(string)) {
	tracked, err := gitOutput(ctx, repoDir, "ls-files")
	if err != nil {
		return
	}
	for _, path := range strings.Split(strings.TrimSpace(tracked), "\n") {
		if _, ok := managedTweetPathDate(path); ok {
			add(path)
		}
	}
}

func git(ctx context.Context, repoDir string, args ...string) error {
	_, err := gitOutput(ctx, repoDir, args...)
	return err
}

func gitOutput(ctx context.Context, repoDir string, args ...string) (string, error) {
	allArgs := append([]string{"-C", repoDir}, args...)
	cmd := exec.CommandContext(ctx, "git", allArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func ParseInterval(value string) (time.Duration, error) {
	return time.ParseDuration(value)
}
