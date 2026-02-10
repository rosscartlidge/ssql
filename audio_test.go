package ssql

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// generateTestWAV creates a minimal WAV file with a sine wave in memory.
func generateTestWAV(sampleRate, numSamples, bitsPerSample, numChannels int) []byte {
	var buf bytes.Buffer

	bytesPerSample := bitsPerSample / 8
	dataSize := numSamples * bytesPerSample * numChannels
	byteRate := sampleRate * numChannels * bytesPerSample
	blockAlign := numChannels * bytesPerSample

	// RIFF header
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVE")

	// fmt chunk
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(numChannels))
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(&buf, binary.LittleEndian, uint32(byteRate))
	binary.Write(&buf, binary.LittleEndian, uint16(blockAlign))
	binary.Write(&buf, binary.LittleEndian, uint16(bitsPerSample))

	// data chunk
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(dataSize))

	// Generate sine wave samples
	freq := 440.0 // A4 note
	for i := 0; i < numSamples; i++ {
		t := float64(i) / float64(sampleRate)
		amplitude := math.Sin(2 * math.Pi * freq * t)

		for ch := 0; ch < numChannels; ch++ {
			switch bitsPerSample {
			case 8:
				// 8-bit unsigned
				sample := uint8(128 + int(amplitude*127))
				buf.WriteByte(sample)
			case 16:
				// 16-bit signed
				sample := int16(amplitude * 32767)
				binary.Write(&buf, binary.LittleEndian, sample)
			case 24:
				// 24-bit signed
				val := int32(amplitude * 8388607)
				buf.WriteByte(byte(val))
				buf.WriteByte(byte(val >> 8))
				buf.WriteByte(byte(val >> 16))
			case 32:
				// 32-bit signed
				sample := int32(amplitude * 2147483647)
				binary.Write(&buf, binary.LittleEndian, sample)
			}
		}
	}

	return buf.Bytes()
}

// TestReadWAV tests reading a WAV file.
func TestReadWAV(t *testing.T) {
	// Create a temporary WAV file
	wavData := generateTestWAV(44100, 100, 16, 1)

	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "test.wav")
	if err := os.WriteFile(filename, wavData, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Read the WAV file
	records, meta, err := ReadWAV(filename)
	if err != nil {
		t.Fatalf("ReadWAV failed: %v", err)
	}

	// Check metadata
	if meta.SampleRate != 44100 {
		t.Errorf("expected sample rate 44100, got %d", meta.SampleRate)
	}
	if meta.NumChannels != 1 {
		t.Errorf("expected 1 channel, got %d", meta.NumChannels)
	}
	if meta.BitsPerSample != 16 {
		t.Errorf("expected 16 bits per sample, got %d", meta.BitsPerSample)
	}

	// Count records and check values
	count := 0
	for r := range records {
		sample := GetOr(r, "sample", int64(-1))
		amplitude := GetOr(r, "amplitude", 999.0)

		if sample != int64(count) {
			t.Errorf("sample %d: expected index %d, got %d", count, count, sample)
		}
		if amplitude < -1.0 || amplitude > 1.0 {
			t.Errorf("sample %d: amplitude %f out of range [-1.0, 1.0]", count, amplitude)
		}
		count++
	}

	if count != 100 {
		t.Errorf("expected 100 samples, got %d", count)
	}
}

// TestReadWAVStereo tests reading a stereo WAV file with mono mixing.
func TestReadWAVStereo(t *testing.T) {
	wavData := generateTestWAV(44100, 50, 16, 2)

	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "stereo.wav")
	if err := os.WriteFile(filename, wavData, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	records, meta, err := ReadWAV(filename)
	if err != nil {
		t.Fatalf("ReadWAV failed: %v", err)
	}

	if meta.NumChannels != 2 {
		t.Errorf("expected 2 channels, got %d", meta.NumChannels)
	}

	count := 0
	for r := range records {
		amplitude := GetOr(r, "amplitude", 999.0)
		if amplitude < -1.0 || amplitude > 1.0 {
			t.Errorf("sample %d: amplitude %f out of range", count, amplitude)
		}
		count++
	}

	if count != 50 {
		t.Errorf("expected 50 samples, got %d", count)
	}
}

// TestReadWAVChannel tests reading a specific channel from stereo.
func TestReadWAVChannel(t *testing.T) {
	// Generate stereo with different waves on each channel
	var buf bytes.Buffer
	sampleRate := 44100
	numSamples := 100
	bitsPerSample := 16
	numChannels := 2

	bytesPerSample := bitsPerSample / 8
	dataSize := numSamples * bytesPerSample * numChannels

	// RIFF header
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVE")

	// fmt chunk
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(numChannels))
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate*numChannels*bytesPerSample))
	binary.Write(&buf, binary.LittleEndian, uint16(numChannels*bytesPerSample))
	binary.Write(&buf, binary.LittleEndian, uint16(bitsPerSample))

	// data chunk
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(dataSize))

	// Left channel = positive values, right channel = negative values
	for i := 0; i < numSamples; i++ {
		leftSample := int16(i * 100)   // Increasing positive values
		rightSample := int16(-i * 100) // Increasing negative values
		binary.Write(&buf, binary.LittleEndian, leftSample)
		binary.Write(&buf, binary.LittleEndian, rightSample)
	}

	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "stereo_diff.wav")
	if err := os.WriteFile(filename, buf.Bytes(), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Read left channel (0)
	leftRecords, _, err := ReadWAVChannel(filename, 0)
	if err != nil {
		t.Fatalf("ReadWAVChannel(0) failed: %v", err)
	}

	leftCount := 0
	for r := range leftRecords {
		amp := GetOr(r, "amplitude", -999.0)
		// Left channel should have non-negative values (after normalization)
		if leftCount > 0 && amp < 0 {
			t.Errorf("left channel sample %d: expected positive, got %f", leftCount, amp)
		}
		leftCount++
	}

	// Read right channel (1)
	rightRecords, _, err := ReadWAVChannel(filename, 1)
	if err != nil {
		t.Fatalf("ReadWAVChannel(1) failed: %v", err)
	}

	rightCount := 0
	for r := range rightRecords {
		amp := GetOr(r, "amplitude", 999.0)
		// Right channel should have non-positive values (after normalization)
		if rightCount > 0 && amp > 0 {
			t.Errorf("right channel sample %d: expected negative, got %f", rightCount, amp)
		}
		rightCount++
	}

	if leftCount != 100 || rightCount != 100 {
		t.Errorf("expected 100 samples per channel, got left=%d right=%d", leftCount, rightCount)
	}
}

// TestWriteWAV tests writing records to a WAV file.
func TestWriteWAV(t *testing.T) {
	// Create test records with a simple sine wave
	numSamples := 100
	sampleRate := 44100
	freq := 440.0

	records := func(yield func(Record) bool) {
		for i := 0; i < numSamples; i++ {
			t := float64(i) / float64(sampleRate)
			amplitude := math.Sin(2 * math.Pi * freq * t)

			mut := MakeMutableRecord()
			mut = mut.Int("sample", int64(i))
			mut = mut.Float("amplitude", amplitude)

			if !yield(mut.Freeze()) {
				return
			}
		}
	}

	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "output.wav")

	if err := WriteWAV(records, filename, sampleRate); err != nil {
		t.Fatalf("WriteWAV failed: %v", err)
	}

	// Verify the file exists and has content
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatalf("output file not found: %v", err)
	}

	// Should have 44-byte header + 100 samples * 2 bytes = 244 bytes
	expectedSize := int64(44 + numSamples*2)
	if info.Size() != expectedSize {
		t.Errorf("expected file size %d, got %d", expectedSize, info.Size())
	}

	// Read back and verify
	readRecords, meta, err := ReadWAV(filename)
	if err != nil {
		t.Fatalf("ReadWAV failed: %v", err)
	}

	if meta.SampleRate != sampleRate {
		t.Errorf("expected sample rate %d, got %d", sampleRate, meta.SampleRate)
	}
	if meta.NumChannels != 1 {
		t.Errorf("expected 1 channel, got %d", meta.NumChannels)
	}
	if meta.BitsPerSample != 16 {
		t.Errorf("expected 16 bits, got %d", meta.BitsPerSample)
	}

	count := 0
	for range readRecords {
		count++
	}
	if count != numSamples {
		t.Errorf("expected %d samples, got %d", numSamples, count)
	}
}

// TestRoundTrip tests writing and reading back WAV data.
func TestRoundTrip(t *testing.T) {
	// Create original samples
	originalSamples := []float64{0.0, 0.5, 1.0, 0.5, 0.0, -0.5, -1.0, -0.5, 0.0}
	sampleRate := 44100

	records := func(yield func(Record) bool) {
		for i, amp := range originalSamples {
			mut := MakeMutableRecord()
			mut = mut.Int("sample", int64(i))
			mut = mut.Float("amplitude", amp)
			if !yield(mut.Freeze()) {
				return
			}
		}
	}

	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "roundtrip.wav")

	if err := WriteWAV(records, filename, sampleRate); err != nil {
		t.Fatalf("WriteWAV failed: %v", err)
	}

	readRecords, _, err := ReadWAV(filename)
	if err != nil {
		t.Fatalf("ReadWAV failed: %v", err)
	}

	readSamples := []float64{}
	for r := range readRecords {
		amp := GetOr(r, "amplitude", 999.0)
		readSamples = append(readSamples, amp)
	}

	if len(readSamples) != len(originalSamples) {
		t.Fatalf("expected %d samples, got %d", len(originalSamples), len(readSamples))
	}

	// Check that samples are close (16-bit quantization introduces small errors)
	for i, orig := range originalSamples {
		diff := math.Abs(readSamples[i] - orig)
		// 16-bit precision is about 1/32768 ≈ 0.00003
		// Allow some tolerance for rounding
		if diff > 0.0001 {
			t.Errorf("sample %d: expected %f, got %f (diff %f)", i, orig, readSamples[i], diff)
		}
	}
}

// TestExtractSignalFromWAV tests direct signal extraction.
func TestExtractSignalFromWAV(t *testing.T) {
	wavData := generateTestWAV(44100, 100, 16, 1)

	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "signal.wav")
	if err := os.WriteFile(filename, wavData, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	signal, meta, err := ExtractSignalFromWAV(filename)
	if err != nil {
		t.Fatalf("ExtractSignalFromWAV failed: %v", err)
	}

	if len(signal) != 100 {
		t.Errorf("expected 100 samples, got %d", len(signal))
	}

	if meta.SampleRate != 44100 {
		t.Errorf("expected sample rate 44100, got %d", meta.SampleRate)
	}

	// Verify values are in valid range
	for i, v := range signal {
		if v < -1.0 || v > 1.0 {
			t.Errorf("sample %d: value %f out of range", i, v)
		}
	}
}

// TestWAV8Bit tests 8-bit unsigned PCM.
func TestWAV8Bit(t *testing.T) {
	wavData := generateTestWAV(44100, 50, 8, 1)

	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "8bit.wav")
	if err := os.WriteFile(filename, wavData, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	records, meta, err := ReadWAV(filename)
	if err != nil {
		t.Fatalf("ReadWAV failed: %v", err)
	}

	if meta.BitsPerSample != 8 {
		t.Errorf("expected 8 bits, got %d", meta.BitsPerSample)
	}

	count := 0
	for r := range records {
		amp := GetOr(r, "amplitude", 999.0)
		if amp < -1.0 || amp > 1.0 {
			t.Errorf("sample %d: amplitude %f out of range", count, amp)
		}
		count++
	}

	if count != 50 {
		t.Errorf("expected 50 samples, got %d", count)
	}
}

// TestWAV24Bit tests 24-bit signed PCM.
func TestWAV24Bit(t *testing.T) {
	wavData := generateTestWAV(48000, 50, 24, 1)

	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "24bit.wav")
	if err := os.WriteFile(filename, wavData, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	records, meta, err := ReadWAV(filename)
	if err != nil {
		t.Fatalf("ReadWAV failed: %v", err)
	}

	if meta.BitsPerSample != 24 {
		t.Errorf("expected 24 bits, got %d", meta.BitsPerSample)
	}
	if meta.SampleRate != 48000 {
		t.Errorf("expected 48000 Hz, got %d", meta.SampleRate)
	}

	count := 0
	for r := range records {
		amp := GetOr(r, "amplitude", 999.0)
		if amp < -1.0 || amp > 1.0 {
			t.Errorf("sample %d: amplitude %f out of range", count, amp)
		}
		count++
	}

	if count != 50 {
		t.Errorf("expected 50 samples, got %d", count)
	}
}

// TestWAV32Bit tests 32-bit signed PCM.
func TestWAV32Bit(t *testing.T) {
	wavData := generateTestWAV(96000, 50, 32, 1)

	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "32bit.wav")
	if err := os.WriteFile(filename, wavData, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	records, meta, err := ReadWAV(filename)
	if err != nil {
		t.Fatalf("ReadWAV failed: %v", err)
	}

	if meta.BitsPerSample != 32 {
		t.Errorf("expected 32 bits, got %d", meta.BitsPerSample)
	}
	if meta.SampleRate != 96000 {
		t.Errorf("expected 96000 Hz, got %d", meta.SampleRate)
	}

	count := 0
	for r := range records {
		amp := GetOr(r, "amplitude", 999.0)
		if amp < -1.0 || amp > 1.0 {
			t.Errorf("sample %d: amplitude %f out of range", count, amp)
		}
		count++
	}

	if count != 50 {
		t.Errorf("expected 50 samples, got %d", count)
	}
}

// TestReadWAVFromReader tests reading from an io.Reader.
func TestReadWAVFromReader(t *testing.T) {
	wavData := generateTestWAV(44100, 100, 16, 1)

	records, meta, err := ReadWAVFromReader(bytes.NewReader(wavData))
	if err != nil {
		t.Fatalf("ReadWAVFromReader failed: %v", err)
	}

	if meta.SampleRate != 44100 {
		t.Errorf("expected sample rate 44100, got %d", meta.SampleRate)
	}

	count := 0
	for range records {
		count++
	}

	if count != 100 {
		t.Errorf("expected 100 samples, got %d", count)
	}
}

// TestWriteWAVToWriter tests writing to an io.Writer.
func TestWriteWAVToWriter(t *testing.T) {
	numSamples := 50
	sampleRate := 22050

	records := func(yield func(Record) bool) {
		for i := 0; i < numSamples; i++ {
			mut := MakeMutableRecord()
			mut = mut.Int("sample", int64(i))
			mut = mut.Float("amplitude", float64(i)/float64(numSamples)-0.5)
			if !yield(mut.Freeze()) {
				return
			}
		}
	}

	var buf bytes.Buffer
	if err := WriteWAVToWriter(records, &buf, sampleRate); err != nil {
		t.Fatalf("WriteWAVToWriter failed: %v", err)
	}

	// Verify we can read it back
	readRecords, meta, err := ReadWAVFromReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadWAVFromReader failed: %v", err)
	}

	if meta.SampleRate != sampleRate {
		t.Errorf("expected sample rate %d, got %d", sampleRate, meta.SampleRate)
	}

	count := 0
	for range readRecords {
		count++
	}

	if count != numSamples {
		t.Errorf("expected %d samples, got %d", numSamples, count)
	}
}

// TestInvalidWAV tests handling of invalid WAV files.
func TestInvalidWAV(t *testing.T) {
	tmpDir := t.TempDir()

	// Test non-existent file
	_, _, err := ReadWAV(filepath.Join(tmpDir, "nonexistent.wav"))
	if err == nil {
		t.Error("expected error for non-existent file")
	}

	// Test empty file
	emptyFile := filepath.Join(tmpDir, "empty.wav")
	if err := os.WriteFile(emptyFile, []byte{}, 0644); err != nil {
		t.Fatalf("failed to create empty file: %v", err)
	}
	_, _, err = ReadWAV(emptyFile)
	if err == nil {
		t.Error("expected error for empty file")
	}

	// Test non-RIFF file
	notRiff := filepath.Join(tmpDir, "notriff.wav")
	if err := os.WriteFile(notRiff, []byte("not a wav file"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	_, _, err = ReadWAV(notRiff)
	if err == nil {
		t.Error("expected error for non-RIFF file")
	}
}

// TestChannelOutOfRange tests requesting an invalid channel.
func TestChannelOutOfRange(t *testing.T) {
	wavData := generateTestWAV(44100, 50, 16, 1) // mono

	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "mono.wav")
	if err := os.WriteFile(filename, wavData, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Request channel 1 from mono file (only channel 0 exists)
	_, _, err := ReadWAVChannel(filename, 1)
	if err == nil {
		t.Error("expected error for channel out of range")
	}

	// Request negative channel
	_, _, err = ReadWAVChannel(filename, -1)
	if err == nil {
		t.Error("expected error for negative channel")
	}
}

// BenchmarkReadWAV benchmarks reading a WAV file.
func BenchmarkReadWAV(b *testing.B) {
	// Generate a larger WAV file for benchmarking
	wavData := generateTestWAV(44100, 44100, 16, 2) // 1 second stereo

	tmpDir := b.TempDir()
	filename := filepath.Join(tmpDir, "bench.wav")
	if err := os.WriteFile(filename, wavData, 0644); err != nil {
		b.Fatalf("failed to write test file: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		records, _, err := ReadWAV(filename)
		if err != nil {
			b.Fatalf("ReadWAV failed: %v", err)
		}
		// Consume all records
		count := 0
		for range records {
			count++
		}
		if count != 44100 {
			b.Fatalf("expected 44100 samples, got %d", count)
		}
	}
}

// BenchmarkExtractSignalFromWAV benchmarks direct signal extraction.
func BenchmarkExtractSignalFromWAV(b *testing.B) {
	wavData := generateTestWAV(44100, 44100, 16, 2) // 1 second stereo

	tmpDir := b.TempDir()
	filename := filepath.Join(tmpDir, "bench.wav")
	if err := os.WriteFile(filename, wavData, 0644); err != nil {
		b.Fatalf("failed to write test file: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		signal, _, err := ExtractSignalFromWAV(filename)
		if err != nil {
			b.Fatalf("ExtractSignalFromWAV failed: %v", err)
		}
		if len(signal) != 44100 {
			b.Fatalf("expected 44100 samples, got %d", len(signal))
		}
	}
}
