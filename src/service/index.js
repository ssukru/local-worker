import * as darwin from "./darwin.js";
import * as linux from "./linux.js";

const impls = { darwin, linux };

/** Platform service backend, or null on unsupported platforms (Windows). */
export const svc = impls[process.platform] ?? null;

export function assertPlatform() {
  if (svc) return;
  throw new Error(
    "local-worker runs natively on macOS and Linux.\n" +
      "On Windows, use WSL2: install a distro (wsl --install -d Ubuntu), then\n" +
      "clone this repo inside WSL and run ./install.sh there. Jobs will run\n" +
      "in Linux — which is what GitHub's ubuntu-* runners are anyway."
  );
}
