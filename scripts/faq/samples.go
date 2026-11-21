package main

import (
	"os"
	"path/filepath"
	"strings"
)

// The FAQ's sample files: the playground's five, plus the codelab's
// signal.csv (a 100 Hz signal, t and amplitude) so a signal-processing
// answer runs. Until 2026-11-21 every fft or spectrogram answer named a
// file the samples lacked (sensor_data.csv, chirp.csv) and was shown as an
// illustration: right, as it happened, but never checked.
var faqOnlySamples = []string{"doc/codelab-data/signal.csv"}

// sampleNotes are what a file's columns do not say. Without the rate, the
// first signal answer ran fft at -rate 1000 and reported frequencies ten
// times too high (2026-11-21); TestSampleNotesMatchTheData checks it.
var sampleNotes = map[string]string{
	"signal.csv": "; sampled at 100 Hz (t steps by 0.01 s), so fft and spectrogram take -rate 100",
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
	files, _ := filepath.Glob(filepath.Join(repo, "cmd", "ssql-playground", "data", "*"))
	for _, f := range faqOnlySamples {
		files = append(files, filepath.Join(repo, f))
	}
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

// readsFAQOnly reports whether a pipeline reads a sample the playground
// does not have: its playground link would open on a missing file.
func readsFAQOnly(pipeline string) bool {
	for _, f := range faqOnlySamples {
		if strings.Contains(pipeline, filepath.Base(f)) {
			return true
		}
	}
	return false
}
