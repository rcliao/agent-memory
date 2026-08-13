package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rcliao/ghost/internal/store"
)

func TestSourceAliasDeclareListRm(t *testing.T) {
	seedMock(t)

	out, err := executeCmd(t, "source", "alias", "Jenny", "--ns", "agent:home", "--canonical", "mami")
	if err != nil {
		t.Fatalf("source alias: %v", err)
	}
	if !strings.Contains(out, "declared") {
		t.Errorf("expected declared status, got: %s", out)
	}

	out, err = executeCmd(t, "source", "list", "--ns", "agent:home")
	if err != nil {
		t.Fatalf("source list: %v", err)
	}
	var aliases []store.SourceAlias
	if err := json.Unmarshal([]byte(out), &aliases); err != nil {
		t.Fatalf("list output not JSON: %v\n%s", err, out)
	}
	if len(aliases) != 1 || aliases[0].Alias != "Jenny" || aliases[0].Canonical != "mami" {
		t.Errorf("unexpected aliases: %+v", aliases)
	}

	if _, err = executeCmd(t, "source", "rm", "Jenny", "--ns", "agent:home"); err != nil {
		t.Fatalf("source rm: %v", err)
	}
	if _, err = executeCmd(t, "source", "rm", "Jenny", "--ns", "agent:home"); err == nil {
		t.Error("removing a missing alias should error")
	}
}

func TestSourceAliasRequiresFlags(t *testing.T) {
	seedMock(t)

	if _, err := executeCmd(t, "source", "alias", "Jenny", "--canonical", "mami"); err == nil {
		t.Error("missing --ns should error")
	}
	if _, err := executeCmd(t, "source", "alias", "Jenny", "--ns", "agent:home"); err == nil {
		t.Error("missing --canonical should error")
	}
	if _, err := executeCmd(t, "source", "list"); err == nil {
		t.Error("list without --ns should error")
	}
}
