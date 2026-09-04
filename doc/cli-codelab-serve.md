# The SSH Operator Console (`ssql serve`)

Part of the [CLI Codelab](cli-codelab.md) — section 8 points here.

`ssql serve` loads one dataset into memory and answers over SSH with
the same command vocabulary — an operator console for a box that holds
the data. Start it in the data's directory; the only setup is an
`authorized_keys` file (your own public key is enough):

```bash
cp ~/.ssh/id_ed25519.pub ./ssql_serve_authorized_keys
ssql serve shuffled.csv -listen 127.0.0.1:2222      # :2222 to accept non-loopback
```

It generates `./ssql_serve_host_key` on first run and prints how many
rows it loaded. Connect with any username — the key authenticates:

```bash
ssh -p 2222 localhost
```

Inside, commands are bare names (no `ssql` prefix), pipelines use `|`,
Tab completes everything including field names, and the loaded dataset
enters a pipeline as **`from-loaded`**:

```
> status                                   # uptime, path, row count
> schema                                   # fields + inferred types
> from-loaded | where -if status eq active | limit 5 | to table
> from-loaded | group-by dept -count n | sort -desc n | to table
> from-loaded | describe | to table
> -help                                    # the command tree
> where -help                              # one command's help
> :help  :set  :history  :exit             # shell built-ins
```

A line that starts with a transform (`where … | to table`) is refused
with the fix spelled out — the dataset only flows from `from-loaded`,
never implicitly. The console carries the data-processing commands and
stream sinks (`to table/csv/tsv/json/jsonl`); file-writing sinks and
file-reading joins are deliberately absent (they would touch
server-side paths). `-session-dir DIR` keeps per-user history and
`:set vi/emacs` across sessions; `-welcome` sets the banner.

The same process can also serve the browser workspace
(`-listen-http 127.0.0.1:8080 -dir DIR`) — see the serve section.
