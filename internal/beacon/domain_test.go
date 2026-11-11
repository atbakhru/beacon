package beacon

import (
	"testing"
	"time"
)

func TestLifecycle(t *testing.T) {
	for _, v := range []struct {
		from, to string
		ok       bool
	}{{"open", "acknowledged", true}, {"open", "resolved", true}, {"acknowledged", "resolved", true}, {"resolved", "open", true}, {"resolved", "acknowledged", false}, {"acknowledged", "open", false}, {"open", "open", false}} {
		if ValidTransition(v.from, v.to) != v.ok {
			t.Errorf("%s -> %s", v.from, v.to)
		}
	}
}
func TestAlertValidation(t *testing.T) {
	now := time.Now()
	valid := AlertInput{SourceEventID: "a", ServiceID: "b", Signature: "pool", Severity: "critical", Summary: "Exhausted"}
	if err := valid.Validate(now); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*AlertInput){func(a *AlertInput) { a.Severity = "fatal" }, func(a *AlertInput) { a.SourceEventID = " " }, func(a *AlertInput) { a.Signature = "" }, func(a *AlertInput) { a.Summary = "" }, func(a *AlertInput) { a.SeenAt = now.Add(time.Hour) }, func(a *AlertInput) { a.SeenAt = now.Add(-31 * 24 * time.Hour) }} {
		a := valid
		mutate(&a)
		if a.Validate(now) == nil {
			t.Error("invalid alert accepted")
		}
	}
}
