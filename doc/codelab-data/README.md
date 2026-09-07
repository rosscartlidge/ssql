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
| `sales_wide.csv` | one row per product, one column per quarter (an empty cell on purpose) | unpivot, pivot |
| `sheet.csv` | a merged-cell spreadsheet export | fill |
| `app.log` | five log lines and one line of garbage | from lines, extract |
| `sensor.csv` | irregular timestamps, temp + rpm | resample, bucket, window |
| `signal.csv` | 5 Hz + 20 Hz at 100 samples/s | fft, spectrogram |

`scripts/codelab-run.sh` executes every bash block of the tutorial in a
directory that `ssql codelab` writes (DFC125); if you change a file, the
runner tells you which example broke.
