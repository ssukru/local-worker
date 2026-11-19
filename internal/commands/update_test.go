package commands

import (
	"context"
	"testing"

	updater "github.com/ssukru/local-worker/internal/update"
)

type fakeUpdateEngine struct {
	plan    updater.Plan
	planErr error
	apply   int
}

func (f *fakeUpdateEngine) Plan(context.Context, string, string, string) (updater.Plan, error) {
	return f.plan, f.planErr
}

func (f *fakeUpdateEngine) Apply(context.Context, updater.Plan) error {
	f.apply++
	return nil
}

func withUpdateEngine(t *testing.T, engine updateEngine) {
	t.Helper()
	old := updateFactory
	updateFactory = func() updateEngine { return engine }
	t.Cleanup(func() { updateFactory = old })
}

func directPlan(comparison int) updater.Plan {
	return updater.Plan{
		CurrentVersion: "0.2.0",
		TargetVersion:  "0.3.0",
		Comparison:     comparison,
		Executable: updater.Executable{
			InvocationPath: "/tmp/lw",
			ResolvedPath:   "/tmp/lw",
			Manager:        updater.ManagerDirect,
		},
	}
}

func TestUpdateCheckDoesNotApply(t *testing.T) {
	engine := &fakeUpdateEngine{plan: directPlan(-1)}
	withUpdateEngine(t, engine)
	flags := NewFlags()
	flags.Set("check", "")
	if err := Update("0.2.0", "lw", nil, flags); err != nil {
		t.Fatal(err)
	}
	if engine.apply != 0 {
		t.Fatalf("Apply called %d times", engine.apply)
	}
}

func TestUpdateYesApplies(t *testing.T) {
	engine := &fakeUpdateEngine{plan: directPlan(-1)}
	withUpdateEngine(t, engine)
	flags := NewFlags()
	flags.Set("yes", "")
	if err := Update("0.2.0", "lw", nil, flags); err != nil {
		t.Fatal(err)
	}
	if engine.apply != 1 {
		t.Fatalf("Apply called %d times", engine.apply)
	}
}

func TestUpdateRejectsHomebrewMutation(t *testing.T) {
	plan := directPlan(-1)
	plan.Executable.Manager = updater.ManagerHomebrew
	engine := &fakeUpdateEngine{plan: plan}
	withUpdateEngine(t, engine)
	flags := NewFlags()
	flags.Set("yes", "")
	if err := Update("0.2.0", "lw", nil, flags); err == nil {
		t.Fatal("Update expected Homebrew error")
	}
	if engine.apply != 0 {
		t.Fatalf("Apply called %d times", engine.apply)
	}
}

func TestUpdateValidatesFlagsBeforePlanning(t *testing.T) {
	engine := &fakeUpdateEngine{plan: directPlan(-1)}
	withUpdateEngine(t, engine)
	flags := NewFlags()
	flags.Set("to", "")
	if err := Update("0.2.0", "lw", nil, flags); err == nil {
		t.Fatal("Update expected missing --to error")
	}
}
