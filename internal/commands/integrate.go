package commands

import (
	"fmt"

	"github.com/ssukru/local-worker/internal/ui"
)

func snippet(fallback string) string {
	return `# --- route this job to your machine, with a one-click hosted fallback ---
#
# 1. Add a "runner" input to workflow_dispatch (safe to add alongside
#    existing inputs). On push events inputs are empty, so the || fallback
#    kicks in and the job lands on your machine by default.

on:
  workflow_dispatch:
    inputs:
      runner:
        description: Where to run this job
        type: choice
        default: self-hosted
        options:
          - self-hosted
          - ` + fallback + `

# 2. Point the job at it:

jobs:
  build:
    runs-on: ${{ inputs.runner || 'self-hosted' }}

# Notes
# - Your machine's runner matches: self-hosted, macOS, ARM64 + your custom labels.
#   To be strict, use a labels array instead:  runs-on: [self-hosted, macOS]
#   (arrays can't express the fallback trick — pick one per workflow.)
# - If the machine is down, queued jobs wait up to 24h; cancel and re-dispatch
#   with runner=` + fallback + ` to use GitHub's cloud instead.
# - Runner label docs: https://docs.github.com/actions/hosting-your-own-runners`
}

// Integrate prints the workflow changes needed to route jobs here.
func Integrate(repo string, flags *Flags) error {
	ui.Intro(" local-worker · integrate ")
	fallback := "ubuntu-latest" // most common; macos-15 for iOS jobs
	fmt.Println(snippet(fallback))
	ui.Info("For iOS jobs use " + ui.Cyan("macos-15") + " as the fallback option instead of ubuntu-latest.")
	if repo != "" {
		ui.Info("Apply this to the workflows in " + ui.Bold(repo) + " that you want on this machine.")
	}
	ui.Outro("")
	return nil
}
