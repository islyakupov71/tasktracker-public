package bot

import (
	"testing"

	"tasktracker/internal/model"
)

func TestMemberIdentityRequiresLocalConfiguration(t *testing.T) {
	t.Setenv("MEMBER_ONE_USERNAME", "")
	t.Setenv("MEMBER_TWO_USERNAME", "")
	if got := nameFromUsername("unknown_account"); got != "" {
		t.Fatalf("unconfigured username resolved to %q", got)
	}
	if got := mention(model.NameMemberOne); got != model.NameMemberOne {
		t.Fatalf("unconfigured mention = %q", got)
	}

	t.Setenv("MEMBER_ONE_USERNAME", " @demo_alpha ")
	t.Setenv("MEMBER_TWO_USERNAME", "demo_beta")
	if got := nameFromUsername("@DEMO_ALPHA"); got != model.NameMemberOne {
		t.Fatalf("configured member was not recognized: %q", got)
	}
	if got := mention(model.NameMemberTwo); got != "@demo_beta" {
		t.Fatalf("configured mention = %q", got)
	}
	if got := nameFromUsername(""); got != "" {
		t.Fatalf("empty username resolved to %q", got)
	}
}
