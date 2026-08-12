# +flag Negation Survey

Reference: DFC073
Created: 2026-03-30
Last modified: 2026-03-30

[Back to Index](./README.md)

All flags across all commands, assessed for whether `+flag` (reverse/negation) has a natural meaning.

## Already Implemented

| Command | Flag | +flag meaning |
|---------|------|---------------|
| where | -if field op value | +if: negate match |
| where | -if-expr expr | +if-expr: negate result |
| update | -if field op value | +if: negate condition |
| update | -if-expr expr | +if-expr: negate result |
| sort | -desc | +desc: ascending |
| sort | -asc | +asc: descending |
| top | -asc | +asc: top N (descending) |
| window | -desc | +desc: ascending |
| merge | -desc | +desc: ascending |

## Should Add Help Text (already work via PrefixHandler boolean negation)

| Command | Flag | +flag meaning |
|---------|------|---------------|
| union | -all | +all: remove duplicates (UNION vs UNION ALL) |
| join | -exclude-left | +exclude-left: include left fields (default) |
| join | -exclude-right | +exclude-right: include right fields (default) |
| correlate | -same | +same: output full length |
| correlate | -auto | +auto: cross-correlate (not auto) |
| convolve | -same | +same: output full length |
| convolve | -auto | +auto: cross-convolve (not auto) |
| fft | -phase | +phase: exclude phase info |

## No Natural Negation

| Command | Flag | Why |
|---------|------|-----|
| from/to | -type | format specifier, not boolean |
| limit/offset | N | numeric, not filterable |
| include/exclude | FIELDS | already inverse commands |
| rename | -as old new | name mapping |
| cast | -type field type | type conversion |
| pivot | -row/-col/-val/-func | structure specifiers |
| group-by | -presorted | optimization hint (negation uncommon) |
| group-by | -rollup/-cube | mode selection (negation uncommon) |
