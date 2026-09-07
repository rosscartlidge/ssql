# Codelab data

The fixture files every example in `doc/cli-codelab.md` runs against —
small enough to read, consistent enough that the whole tutorial works
without ever leaving this directory. They are embedded in the `ssql`
binary (`embed.go`): `ssql codelab [DIR]` writes them out, so a reader
installs ssql and never clones the repository. `ssql codelab -force`
restores them after editing.

| file | what it is | used by |
|---|---|---|
| `employees.csv` / `employees.parquet` | 10 people: dept, salary, city, level, hire_date, status (the parquet is the same rows, for `-records`/`-columns`) | Part 1 throughout, Make it fast |
| `customers.csv` / `orders.csv` | a joinable pair (one order has a customer that does not exist — on purpose) | join, group-by |
| `shards.csv` + `orders_2026-01.csv` / `orders_2026-02.csv` | a shard catalog: `orders.csv` split by month, both shards `local` (on a cluster the host column names SSH hosts) | from catalog |
| `sales_wide.csv` | one row per product, one column per quarter (an empty cell on purpose) | unpivot, pivot |
| `sheet.csv` | a merged-cell spreadsheet export | fill |
| `app.log` | five log lines and one line of garbage | from lines, extract |
| `sensor.csv` | irregular timestamps, temp + rpm | resample, bucket, window |
| `signal.csv` | 5 Hz + 20 Hz at 100 samples/s | fft, spectrogram |

`codelab-run.sh` (here, and written out with the data) executes every
bash block of the tutorial in a directory that `ssql codelab` writes
(DFC125). In the repository it builds ssql from the checkout; beside
your own copy of the data it uses the `ssql` on your PATH and fetches
the codelab for that version — a self-test of your install. If you
change a fixture, the runner tells you which example broke.
