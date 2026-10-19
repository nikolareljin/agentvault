# Machine checks

Checks that need a real machine rather than a unit test. Run them on Linux and
macOS before a release. Exit codes: 0 pass, 1 fail, 2 cannot run here.

| Check | What it proves |
|---|---|
| `install_check.sh` | `./dev install-bin` builds and installs the binary, and the installed binary runs and reports the `VERSION` file's version |
