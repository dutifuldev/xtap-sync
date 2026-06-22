# xtap-sync

`xtap-sync` syncs local xTap JSONL exports into a Git repository. It reads flat
`tweets-YYYY-MM-DD.jsonl` files from an xTap output directory, deduplicates tweet
records by `id`, and writes normalized archive files under
`data/tweets/YYYY/MM/` in the target repository.

The target repository is configurable. It can be a private data store, a public
archive, or any Git checkout that `git push` can update.

## Install

```sh
go install github.com/dutifuldev/xtap-sync/cmd/xtap-sync@latest
```

From a local checkout:

```sh
go install ./cmd/xtap-sync
```

## Sync Once

```sh
xtap-sync sync --source "$HOME/Downloads/xtap" --repo "$HOME/repos/my-xtap-data"
```

If the target repository has an `origin` remote, `xtap-sync` fetches that branch,
merges remote records with local xTap records, commits changed archive files, and
pushes. Use `--no-push` for local-only testing:

```sh
xtap-sync sync --source "$HOME/Downloads/xtap" --repo "$HOME/repos/my-xtap-data" --no-push
```

## Configuration

By default, `xtap-sync` looks for:

```text
$XDG_CONFIG_HOME/xtap-sync/config.json
```

If `XDG_CONFIG_HOME` is unset, it uses:

```text
$HOME/.config/xtap-sync/config.json
```

Example:

```json
{
  "source_dir": "~/Downloads/xtap",
  "repo_dir": "~/repos/my-xtap-data",
  "remote": "origin",
  "branch": "main",
  "commit_message": "sync xTap tweets",
  "push": true,
  "service_label": "dev.xtap-sync",
  "interval": "1h"
}
```

Command-line flags override the config file. You can also choose another config
file:

```sh
xtap-sync sync --config ./xtap-sync.json
```

Useful environment variables:

- `XTAP_SYNC_CONFIG`: default config file path
- `XTAP_SYNC_SOURCE_DIR`: default xTap output directory
- `XTAP_SYNC_REPO_DIR`: default target Git checkout
- `XTAP_OUTPUT_DIR`: fallback xTap output directory

## Background Sync

On macOS, install an hourly LaunchAgent:

```sh
xtap-sync install-service --config "$HOME/.config/xtap-sync/config.json"
```

Without a config file:

```sh
xtap-sync install-service --source "$HOME/Downloads/xtap" --repo "$HOME/repos/my-xtap-data"
```

The service logs to:

```text
$HOME/Library/Logs/xtap-sync/
```

Remove it with:

```sh
xtap-sync uninstall-service
```

## Verify

Check whether all source tweet IDs are present in the target repository:

```sh
xtap-sync verify --config "$HOME/.config/xtap-sync/config.json"
```

The command reports source IDs, repository IDs, missing IDs, and extra repository
IDs. It exits non-zero when source tweets are missing from the target repository.

## Storage Backends

`xtap-sync` currently syncs to Git repositories, including GitHub-hosted
repositories. If GitHub storage becomes a bottleneck, a future backend could sync
the same normalized archive files to object storage such as Hugging Face buckets.

## Data Policy

Only `data/tweets/YYYY/MM/tweets-YYYY-MM-DD.jsonl` files are managed in the target
repository. xTap source files are read from a flat `tweets-YYYY-MM-DD.jsonl`
output directory. Media folders, partial downloads, logs, credentials, and common
binary media files are ignored.

## License

[MIT](LICENSE)
