package features

import "testing"

func TestActivityActionVocabulary(t *testing.T) {
	for _, action := range []string{"", "all"} {
		if got := NormalizeAction(action); got != ActionAll {
			t.Fatalf("NormalizeAction(%q) = %q, want %q", action, got, ActionAll)
		}
	}
	for _, action := range []string{"keepalive", "activity"} {
		if got := NormalizeAction(action); got != ActionActivity {
			t.Fatalf("NormalizeAction(%q) = %q, want %q", action, got, ActionActivity)
		}
	}
	if got := NormalizeAction("checkin"); got != ActionCheckin {
		t.Fatalf("NormalizeAction(checkin) = %q", got)
	}
	if got := NormalizeAction("unknown"); got != "unknown" {
		t.Fatalf("unknown action must be preserved, got %q", got)
	}
	for _, action := range []string{"", "all", "checkin", "activity", "keepalive"} {
		if !ValidAction(action) {
			t.Fatalf("action %q must be valid", action)
		}
	}
	if ValidAction("unknown") {
		t.Fatal("unknown action must be rejected")
	}
	if OperationAction(OperationActivity) != ActionActivity {
		t.Fatalf("activity operation must map back to activity action")
	}
	if OperationAction(OperationCheckin) != ActionCheckin {
		t.Fatalf("checkin operation must map back to checkin action")
	}
}
