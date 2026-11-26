package main

import (
	"os"
	"path/filepath"
)

// The FAQ's sample files are the playground's (cmd/ssql-playground/data),
// so every answer over them opens there and runs. signal.csv (a 100 Hz
// signal, t and amplitude) joined them for the FAQ on 2026-11-21: until
// then every fft or spectrogram answer named a file the samples lacked
// (sensor_data.csv, chirp.csv) and was shown as an illustration. It lived
// only in the FAQ, without playground links, until 2026-11-24, when it
// moved into the playground's data; TestSamplesAreThePlaygrounds keeps
// the two one set.
const samplesDir = "cmd/ssql-playground/data"

// sampleNotes are what a file's columns do not say. Without the rate, the
// first signal answer ran fft at -rate 1000 and reported frequencies ten
// times too high (2026-11-21); TestSampleNotesMatchTheData checks it.
var sampleNotes = map[string]string{
	"signal.csv": "; sampled at 100 Hz (t steps by 0.01 s), so fft and spectrogram take -rate 100",
	"cast.csv":   `; one row per actor per film, real casts from Wikidata; a movie is written "Title (year)", such as "Apollo 13 (1995)", a person by name alone. The actors and films form an undirected graph (traverse -both)`,
}

// buildSamples copies the sample files into one directory under the user
// cache, replacing what was there, and returns it.
func buildSamples(repo string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "ssql-faq", "samples")
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	files, _ := filepath.Glob(filepath.Join(repo, samplesDir, "*"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(f)), b, 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}
