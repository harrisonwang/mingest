package ingest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestComputeTaskIDStableAndIgnoresFragment(t *testing.T) {
	a := computeTaskID("HTTPS://YouTube.com/watch?v=abc#comments")
	b := computeTaskID("https://youtube.com/watch?v=abc")
	if a != b {
		t.Fatalf("expected normalized URLs to share task_id: %s != %s", a, b)
	}
	if len(a) <= len("tsk_") || a[:4] != "tsk_" {
		t.Fatalf("unexpected task_id format: %s", a)
	}
}

func TestParseGetOptionsBatchJSONL(t *testing.T) {
	opts, err := parseGetOptions([]string{"--batch", "urls.txt", "--continue-on-error", "--jsonl", "--output-dir", "videos"})
	if err != nil {
		t.Fatalf("parseGetOptions returned error: %v", err)
	}
	if opts.BatchFile != "urls.txt" || !opts.ContinueOnError || !opts.JSONL || opts.OutDir != "videos" {
		t.Fatalf("unexpected opts: %+v", opts)
	}
}

func TestParseGetOptionsRejectsBatchJSON(t *testing.T) {
	if _, err := parseGetOptions([]string{"--batch", "urls.txt", "--json"}); err == nil {
		t.Fatal("expected --batch with --json to be rejected")
	}
}

func TestParseAuthOptionsValidateURL(t *testing.T) {
	opts, err := parseAuthOptions([]string{"validate", "youtube", "--url", "https://youtube.com/watch?v=abc", "--json"})
	if err != nil {
		t.Fatalf("parseAuthOptions returned error: %v", err)
	}
	if opts.Action != "validate" || opts.Platform != "youtube" || opts.ValidateURL == "" || !opts.JSON {
		t.Fatalf("unexpected opts: %+v", opts)
	}
}

func TestClassifyFailureBotCheck(t *testing.T) {
	failure := classifyFailure("ERROR: [youtube] abc: Sign in to confirm you're not a bot", youtubePlatform())
	if failure.ExitCode != exitAuthRequired {
		t.Fatalf("unexpected exit code: %d", failure.ExitCode)
	}
	if failure.ErrorCode != errorBotCheck {
		t.Fatalf("unexpected error code: %s", failure.ErrorCode)
	}
	if failure.RecommendedCommand != "mingest auth login youtube" {
		t.Fatalf("unexpected recommended command: %s", failure.RecommendedCommand)
	}
}

func TestFilterResultRecordsFailedAndMissing(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "ok.mp4")
	if err := writeTestFile(existing); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	records := []resultRecord{
		{TaskID: "tsk_ok", SourceURL: "https://example.com/a", Platform: "youtube", OK: true, FilePath: existing},
		{TaskID: "tsk_missing", SourceURL: "https://example.com/b", Platform: "youtube", OK: true, FilePath: filepath.Join(dir, "missing.mp4")},
		{TaskID: "tsk_failed", SourceURL: "https://example.com/c", Platform: "bilibili", OK: false, ErrorCode: errorAuthRequired},
	}

	failed := filterResultRecords(records, lsOptions{Failed: true})
	if len(failed) != 1 || failed[0].TaskID != "tsk_failed" {
		t.Fatalf("unexpected failed filter result: %+v", failed)
	}
	missing := filterResultRecords(records, lsOptions{Missing: true})
	if len(missing) != 1 || missing[0].TaskID != "tsk_missing" {
		t.Fatalf("unexpected missing filter result: %+v", missing)
	}
}

func writeTestFile(path string) error {
	return os.WriteFile(path, []byte("ok"), 0o600)
}
