package broadcast

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestDiscordLibrariesLogOnlyWarningsWhileOurStatsLogAtInfo(t *testing.T) {
	var buf bytes.Buffer
	ours := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	theirs := slog.New(warnOnly{ours.Handler()}).With("name", "bot").WithGroup("voice")
	theirs.Info("Creating new voice conn")
	theirs.Warn("failed to reconnect voice gateway")
	ours.Info("broadcast", "stats", "slots=250")
	got := buf.String()
	if strings.Contains(got, "Creating new voice conn") {
		t.Fatalf("disgo info reached the log: %s", got)
	}
	for _, want := range []string{"failed to reconnect", "slots=250"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log lacks %q: %s", want, got)
		}
	}
}
