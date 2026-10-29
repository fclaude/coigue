package modsrc

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveGitHub checks the client against the real github.com. It is off
// unless COIGUE_LIVE_GITHUB=1, so the tests do not depend on the network or on
// GitHub's rate limits.
func TestLiveGitHub(t *testing.T) {
	if os.Getenv("COIGUE_LIVE_GITHUB") != "1" {
		t.Skip("set COIGUE_LIVE_GITHUB=1 to talk to github.com")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := NewClient()
	src := Source{Owner: "fclaude", Repo: "brote", Ref: "v0.4.0"}

	commit, err := c.Resolve(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if commit != "f5bbf654de1e18b7dc848bf4324355c4e4b7e29a" {
		t.Fatalf("v0.4.0 resolved to %s", commit)
	}
	files, err := c.Fetch(ctx, src, commit)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s at %s has %d modules", src.Slug(), commit[:7], len(files))

	if _, err := c.Resolve(ctx, Source{Owner: "fclaude", Repo: "brote", Ref: "no-such-ref-anywhere"}); err == nil {
		t.Fatal("a ref that does not exist resolved")
	}
	if _, err := c.Resolve(ctx, Source{Owner: "fclaude", Repo: "no-such-repository-anywhere"}); err == nil {
		t.Fatal("a repository that does not exist resolved")
	}
}
