import * as p from "@clack/prompts";
import pc from "picocolors";

const SNIPPET = (
  fallback
) => `# --- route this job to your Mac, with a one-click hosted fallback ---
#
# 1. Add a "runner" input to workflow_dispatch (safe to add alongside
#    existing inputs). On push events inputs are empty, so the || fallback
#    kicks in and the job lands on your Mac by default.

on:
  workflow_dispatch:
    inputs:
      runner:
        description: Where to run this job
        type: choice
        default: self-hosted
        options:
          - self-hosted
          - ${fallback}

# 2. Point the job at it:

jobs:
  build:
    runs-on: \${{ inputs.runner || 'self-hosted' }}

# Notes
# - Your Mac's runner matches: self-hosted, macOS, ARM64 + your custom labels.
#   To be strict, use a labels array instead:  runs-on: [self-hosted, macOS]
#   (arrays can't express the fallback trick — pick one per workflow.)
# - If the Mac is down, queued jobs wait up to 24h; cancel and re-dispatch
#   with runner=${fallback} to use GitHub's cloud instead.
# - Runner label docs: https://docs.github.com/actions/hosting-your-own-runners`;

export async function integrate(repo) {
  p.intro(pc.bgCyan(pc.black(" local-worker · integrate ")));
  const fallback = "ubuntu-latest"; // most common; macos-15 for iOS jobs
  console.log(SNIPPET(fallback));
  p.log.info(
    `For iOS jobs use ${pc.cyan("macos-15")} as the fallback option instead of ubuntu-latest.`
  );
  if (repo) {
    p.log.info(`Apply this to the workflows in ${pc.bold(repo)} that you want on this Mac.`);
  }
  p.outro("");
}
