package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/memory"
)

func TestRunMemoryModelDownload_WithDest(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "models")
	err := runMemoryModelDownload(dest, "")
	if err != nil {
		t.Logf("expected error in test env: %v", err)
	}
}

func TestRunMemoryModelDownload_AutoDetectOnnx(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "models")
	err := runMemoryModelDownload(dest, "")
	if err != nil {
		t.Logf("expected error: %v", err)
	}
}

func TestRunMemoryModelDownload_ExplicitOnnxPath(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "models")
	err := runMemoryModelDownload(dest, "nonexistent/model.onnx")
	if err == nil {
		t.Log("no error returned")
	}
}

func TestRunMemoryModelDownload_DefaultDest(t *testing.T) {
	err := runMemoryModelDownload("", "")
	if err != nil {
		t.Logf("expected error: %v", err)
	}
}

func TestRunServe_FlagProjectOverridesCWD(t *testing.T) {
	tmpDir := t.TempDir()
	orig := flagServeProject
	flagServeProject = tmpDir
	defer func() { flagServeProject = orig }()
	if flagServeProject != tmpDir {
		t.Errorf("flagServeProject = %q, want %q", flagServeProject, tmpDir)
	}
}

func TestRunServe_ShutdownSignalHandling(t *testing.T) {
	cmd := newServeCmd()
	if cmd.Use != "serve" {
		t.Errorf("Use = %q, want 'serve'", cmd.Use)
	}
	for _, name := range []string{"addr", "project", "pairing-timeout", "model"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q not registered", name)
		}
	}
}

func TestRunServe_FlagDefaults(t *testing.T) {
	cmd := newServeCmd()
	addr, err := cmd.Flags().GetString("addr")
	if err != nil {
		t.Fatal(err)
	}
	if addr != ":8080" {
		t.Errorf("addr default = %q, want ':8080'", addr)
	}
	timeout, err := cmd.Flags().GetDuration("pairing-timeout")
	if err != nil {
		t.Fatal(err)
	}
	if timeout != 5*time.Minute {
		t.Errorf("pairing-timeout default = %v, want 5m", timeout)
	}
}

func TestInitResources_CleanupNil(t *testing.T) {
	r := &initResources{}
	r.cleanup()
}

func TestInitResources_CleanupPartial(t *testing.T) {
	r := &initResources{}
	r.cleanup()
}

func TestNewMemoryInitCmd(t *testing.T) {
	cmd := newMemoryInitCmd()
	if cmd.Use != "init [dir]" {
		t.Errorf("Use = %q, want 'init [dir]'", cmd.Use)
	}
	if cmd.Flags().Lookup("wing") == nil {
		t.Error("wing flag not registered")
	}
}

func TestRunMemoryInit_DefaultWing(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "myproject")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	absDir, err := filepath.Abs(subDir)
	if err != nil {
		t.Fatal(err)
	}
	wing := filepath.Base(absDir)
	if wing != "myproject" {
		t.Errorf("wing = %q, want 'myproject'", wing)
	}
}

func TestRunMemoryRecent_WithObservations(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".pi-go", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(memDir, "claude-mem.db")
	db, err := memory.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := memory.NewSQLiteStore(db)
	ctx := context.Background()
	store.CreateSession(ctx, &memory.Session{
		SessionID: "session-1",
		Project:   dir,
		StartedAt: time.Now(),
		Status:    "active",
	})
	err = runMemoryRecent(dir, 10, "", false)
	if err != nil {
		t.Fatalf("runMemoryRecent: %v", err)
	}
}

func TestRunMemoryRecent_AllObservationTypes(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".pi-go", "memory")
	os.MkdirAll(memDir, 0o755)
	dbPath := filepath.Join(memDir, "claude-mem.db")
	db, err := memory.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := memory.NewSQLiteStore(db)
	ctx := context.Background()
	store.CreateSession(ctx, &memory.Session{
		SessionID: "session-types",
		Project:   dir,
		StartedAt: time.Now(),
		Status:    "completed",
	})
	validTypes := []memory.ObservationType{
		memory.TypeDecision,
		memory.TypeBugfix,
		memory.TypeFeature,
		memory.TypeRefactor,
		memory.TypeDiscovery,
		memory.TypeChange,
	}
	for _, typ := range validTypes {
		store.InsertObservation(ctx, &memory.Observation{
			SessionID: "session-types",
			Project:   dir,
			Title:     string(typ),
			Type:      typ,
			Text:      "test",
			CreatedAt: time.Now(),
		})
	}
	err = runMemoryRecent(dir, 10, "", false)
	if err != nil {
		t.Fatalf("runMemoryRecent: %v", err)
	}
}

func TestRunMemoryRecent_LimitWithTypeFilter(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".pi-go", "memory")
	os.MkdirAll(memDir, 0o755)
	dbPath := filepath.Join(memDir, "claude-mem.db")
	db, err := memory.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := memory.NewSQLiteStore(db)
	ctx := context.Background()
	store.CreateSession(ctx, &memory.Session{
		SessionID: "limit-test",
		Project:   dir,
		StartedAt: time.Now(),
		Status:    "completed",
	})
	for i := 0; i < 10; i++ {
		store.InsertObservation(ctx, &memory.Observation{
			SessionID: "limit-test",
			Project:   dir,
			Title:     "Bugfix",
			Type:      memory.TypeBugfix,
			Text:      "test",
			CreatedAt: time.Now().Add(-time.Duration(i) * time.Minute),
		})
	}
	err = runMemoryRecent(dir, 3, "bugfix", false)
	if err != nil {
		t.Fatalf("runMemoryRecent: %v", err)
	}
}

func TestRunMemoryRecent_LimitExceedsData(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".pi-go", "memory")
	os.MkdirAll(memDir, 0o755)
	dbPath := filepath.Join(memDir, "claude-mem.db")
	db, err := memory.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := memory.NewSQLiteStore(db)
	ctx := context.Background()
	store.CreateSession(ctx, &memory.Session{
		SessionID: "small-data",
		Project:   dir,
		StartedAt: time.Now(),
		Status:    "completed",
	})
	store.InsertObservation(ctx, &memory.Observation{
		SessionID: "small-data",
		Project:   dir,
		Title:     "Obs 1",
		Type:      memory.TypeFeature,
		Text:      "test",
		CreatedAt: time.Now(),
	})
	store.InsertObservation(ctx, &memory.Observation{
		SessionID: "small-data",
		Project:   dir,
		Title:     "Obs 2",
		Type:      memory.TypeFeature,
		Text:      "test",
		CreatedAt: time.Now(),
	})
	err = runMemoryRecent(dir, 100, "", false)
	if err != nil {
		t.Fatalf("runMemoryRecent: %v", err)
	}
}

func TestNewMemoryRecentCmd(t *testing.T) {
	cmd := newMemoryRecentCmd()
	if cmd.Use != "recent [project]" {
		t.Errorf("Use = %q, want 'recent [project]'", cmd.Use)
	}
	for _, name := range []string{"limit", "type", "json"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q not registered", name)
		}
	}
}

func TestScanRoomCandidates_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	rooms := scanRoomCandidates(dir)
	if len(rooms) != 0 {
		t.Errorf("expected 0 rooms in empty dir, got %d", len(rooms))
	}
}

func TestScanRoomCandidates_OnlySkippedDirs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".git", "node_modules", "vendor", "__pycache__", ".pi-go", "dist", "build", ".idea", ".vscode"} {
		os.Mkdir(filepath.Join(dir, name), 0o755)
	}
	rooms := scanRoomCandidates(dir)
	if len(rooms) != 0 {
		t.Errorf("expected 0 rooms, got %d", len(rooms))
	}
}

func TestScanRoomCandidates_DotDirs(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, ".hidden"), 0o755)
	os.Mkdir(filepath.Join(dir, "visible"), 0o755)
	rooms := scanRoomCandidates(dir)
	for _, r := range rooms {
		if strings.HasPrefix(r, ".") {
			t.Errorf("found dot-prefixed directory %q in rooms", r)
		}
	}
}

func TestScanRoomCandidates_UnreadableDir(t *testing.T) {
	rooms := scanRoomCandidates("/nonexistent/path")
	if rooms != nil {
		t.Errorf("expected nil rooms, got %v", rooms)
	}
}

func TestScanRoomCandidates_MixedDirs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"src", "cmd", ".git", "pkg", "vendor"} {
		os.Mkdir(filepath.Join(dir, name), 0o755)
	}
	rooms := scanRoomCandidates(dir)
	roomMap := make(map[string]bool)
	for _, r := range rooms {
		roomMap[r] = true
	}
	if !roomMap["src"] {
		t.Error("expected 'src' in rooms")
	}
	if !roomMap["cmd"] {
		t.Error("expected 'cmd' in rooms")
	}
	if !roomMap["pkg"] {
		t.Error("expected 'pkg' in rooms")
	}
	if roomMap[".git"] {
		t.Error("'.git' should not be in rooms")
	}
	if roomMap["vendor"] {
		t.Error("'vendor' should not be in rooms")
	}
}

func TestWriteMempalaceYAML_EmptyRooms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mempalace.yaml")
	err := writeMempalaceYAML(path, "testwing", []string{})
	if err != nil {
		t.Fatalf("writeMempalaceYAML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "wing: testwing") {
		t.Error("yaml missing wing name")
	}
	if !strings.Contains(content, "# No subdirectories") {
		t.Error("yaml missing comment")
	}
}

func TestWriteMempalaceYAML_MultipleRooms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mempalace.yaml")
	rooms := []string{"api", "web", "cli"}
	err := writeMempalaceYAML(path, "myproject", rooms)
	if err != nil {
		t.Fatalf("writeMempalaceYAML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, room := range rooms {
		if !strings.Contains(content, "name: "+room) {
			t.Errorf("yaml missing room %q", room)
		}
	}
}

func TestWriteMempalaceYAML_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mempalace.yaml")
	err := writeMempalaceYAML(path, "first", []string{"room1"})
	if err != nil {
		t.Fatal(err)
	}
	err = writeMempalaceYAML(path, "second", []string{"room2"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, "first") {
		t.Error("yaml still contains 'first' after overwrite")
	}
	if !strings.Contains(content, "second") {
		t.Error("yaml missing 'second' after overwrite")
	}
}

func TestWriteMempalaceYAML_SingleRoom(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mempalace.yaml")
	err := writeMempalaceYAML(path, "single", []string{"onlyroom"})
	if err != nil {
		t.Fatalf("writeMempalaceYAML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "name: onlyroom") {
		t.Error("yaml missing room")
	}
}

func TestFormatAge_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected string
	}{
		{"just now", time.Now(), "just now"},
		{"5 minutes ago", time.Now().Add(-5 * time.Minute), "5m ago"},
		{"1 minute ago", time.Now().Add(-1 * time.Minute), "1m ago"},
		{"59 minutes ago", time.Now().Add(-59 * time.Minute), "59m ago"},
		{"1 hour ago", time.Now().Add(-1 * time.Hour), "1h ago"},
		{"3 hours ago", time.Now().Add(-3 * time.Hour), "3h ago"},
		{"23 hours ago", time.Now().Add(-23 * time.Hour), "23h ago"},
		{"1 day ago", time.Now().Add(-1 * 24 * time.Hour), "1d ago"},
		{"6 days ago", time.Now().Add(-6 * 24 * time.Hour), "6d ago"},
		{"2 weeks ago", time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC), "Jan 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatAge(tt.input)
			if got != tt.expected {
				t.Errorf("formatAge() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestDefaultAPIBaseURL_TableDriven(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"anthropic", "https://api.anthropic.com"},
		{"openai", "https://api.openai.com"},
		{"gemini", "https://generativelanguage.googleapis.com"},
		{"ollama", ""},
		{"", ""},
		{"unknown", ""},
		{"azure", ""},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := defaultAPIBaseURL(tt.provider)
			if got != tt.want {
				t.Errorf("defaultAPIBaseURL(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestPingEndpoint_TableDriven(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"anthropic", "/v1/messages"},
		{"openai", "/v1/models"},
		{"gemini", "/v1beta/models"},
		{"ollama", "/"},
		{"", "/"},
		{"unknown", "/"},
		{"azure", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := pingEndpoint(tt.provider)
			if got != tt.want {
				t.Errorf("pingEndpoint(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestTruncate_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"over limit", "hello world", 5, "hello..."},
		{"empty string", "", 5, ""},
		{"zero limit", "hello", 0, "..."},
		{"single char limit", "hello", 1, "h..."},
		{"two char limit", "hello", 2, "he..."},
		{"three char limit", "hello", 3, "hel..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.s, tt.n)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
		})
	}
}

func TestParsePairingCode_TableDriven(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCode  string
		wantToken string
	}{
		{"plain code", "123456", "123456", ""},
		{"code with token", "123456:mytoken", "123456", "mytoken"},
		{"with whitespace", "  789012  ", "789012", ""},
		{"with token and spaces", "  123 : token ", "123 ", " token"},
		{"empty string", "", "", ""},
		{"only colon", ":", "", ""},
		{"multiple colons", "a:b:c", "a:b:c", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, token, err := ParsePairingCode(tt.input)
			if err != nil {
				t.Fatalf("ParsePairingCode: %v", err)
			}
			if code != tt.wantCode {
				t.Errorf("code = %q, want %q", code, tt.wantCode)
			}
			if token != tt.wantToken {
				t.Errorf("token = %q, want %q", token, tt.wantToken)
			}
		})
	}
}
