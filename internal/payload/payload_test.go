package payload

import "testing"

func TestDecodeFullPayload(t *testing.T) {
	p, err := Decode([]byte(`{
		"model": {"display_name": "Opus 5.5 (1M context)"},
		"effort": {"level": "medium"},
		"fast_mode": false,
		"workspace": {"current_dir": "C:\\x\\techcentral", "project_dir": "C:\\x\\techcentral"},
		"rate_limits": {"five_hour": {"used_percentage": 21, "resets_at": 1790283000},
		                "seven_day": {"used_percentage": 5, "resets_at": 1790838000}},
		"context_window": {"used_percentage": 8},
		"prompt_cache": {"expires_at": 1790277013, "recache_tokens_if_cold": 82336},
		"cost": {"total_duration_ms": 460272},
		"some_future_field": {"nested": [1, 2, 3]}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Model.DisplayName != (Str{"Opus 5.5 (1M context)", true}) {
		t.Errorf("display_name = %+v", p.Model.DisplayName)
	}
	if p.RateLimits.SevenDay.ResetsAt != (Num{1790838000, true}) {
		t.Errorf("resets_at = %+v", p.RateLimits.SevenDay.ResetsAt)
	}
	if p.Workspace.CurrentDir.V != `C:\x\techcentral` {
		t.Errorf("current_dir = %q", p.Workspace.CurrentDir.V)
	}
	if p.FastMode != (Bool{false, true}) {
		t.Errorf("fast_mode = %+v", p.FastMode)
	}
}

func TestDecodeLeavesWrongTypedFieldsUnset(t *testing.T) {
	p, err := Decode([]byte(`{
		"model": {"display_name": 42},
		"fast_mode": "yes",
		"rate_limits": {"five_hour": {"used_percentage": "90; rm -rf /", "resets_at": null},
		                "seven_day": "oops"},
		"context_window": {"used_percentage": 72}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Model.DisplayName.Set || p.FastMode.Set || p.RateLimits.FiveHour.UsedPercentage.Set ||
		p.RateLimits.FiveHour.ResetsAt.Set {
		t.Errorf("a wrong-typed leaf was set: %+v", p)
	}
	if p.RateLimits.SevenDay != nil && p.RateLimits.SevenDay.UsedPercentage.Set {
		t.Error("a string in place of an object produced a value")
	}
	if p.ContextWindow.UsedPercentage != (Num{72, true}) {
		t.Error("a valid field after the bad ones was lost")
	}
}

func TestDecodeRejectsNonObjects(t *testing.T) {
	for _, in := range []string{"", "not json", `{"model":`, `[]`, `"x"`, `42`} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("Decode(%q) succeeded", in)
		}
	}
}

func TestDecodeAcceptsNullAndEmpty(t *testing.T) {
	for _, in := range []string{`{}`, `null`} {
		if _, err := Decode([]byte(in)); err != nil {
			t.Errorf("Decode(%q): %v", in, err)
		}
	}
}
