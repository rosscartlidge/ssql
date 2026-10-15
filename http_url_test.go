package ssql

import "testing"

func TestHTTPURLExt(t *testing.T) {
	cases := map[string]string{
		"https://h/p/x.csv":                     ".csv",
		"https://h/p/x.parquet?X-Amz-Sig=a.b.c": ".parquet",
		"https://h/p/x.CSV#frag":                ".csv",
		"https://h/p/noext":                     "",
		"https://h/p.dir/noext":                 "",
	}
	for u, want := range cases {
		if got := HTTPURLExt(u); got != want {
			t.Errorf("HTTPURLExt(%q) = %q, want %q", u, got, want)
		}
	}
}
