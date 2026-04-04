Output doc/demo-optimize.gif

Set Shell "bash"
Set FontSize 14
Set Width 900
Set Height 550
Set Padding 10
Set Theme "TokyoNight"

Type "# A naive pipeline with redundant steps"
Enter
Sleep 500ms

Type "ssql from test-data/users.csv | ssql where -if age gt 25 | ssql where -if dept eq Engineering | ssql sort salary -desc | ssql limit 3 | ssql to table"
Enter
Sleep 2s

Type ""
Enter
Type "# Optimize it — merges filters, collapses sort+limit to top"
Enter
Sleep 500ms

Type "(export SSQLGO=1; ssql from test-data/users.csv | ssql where -if age gt 25 | ssql where -if dept eq Engineering | ssql sort salary -desc | ssql limit 3 | ssql to table) | ssql generate ssql -explain"
Enter
Sleep 3s

Type ""
Enter
Type "# Compile the optimized pipeline to standalone Go code"
Enter
Sleep 500ms

Type "(export SSQLGO=1; ssql from test-data/users.csv | ssql where -if dept eq Engineering -if age gt 25 | ssql top 3 -field salary | ssql to table) | ssql generate go | head -30"
Enter
Sleep 3s

Type ""
Enter
