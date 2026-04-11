Output doc/demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 900
Set Height 500
Set Padding 10
Set Theme "TokyoNight"

Type "# Read CSV, filter, and display as a table"
Enter
Sleep 500ms

Type "ssql from test-data/users.csv | ssql where -if dept eq Engineering | ssql to table"
Enter
Sleep 2s

Type ""
Enter
Type "# Group by department with aggregations"
Enter
Sleep 500ms

Type "ssql from test-data/users.csv | ssql group-by dept -count cnt -avg salary avg_sal | ssql sort -desc cnt | ssql to table"
Enter
Sleep 2s

Type ""
Enter
Type "# Generate DuckDB SQL from the same pipeline"
Enter
Sleep 500ms

Type "(export SSQLGO=1; ssql from test-data/users.csv | ssql where -if age gt 30 | ssql group-by dept -count cnt | ssql to table) | ssql generate sql"
Enter
Sleep 8s
