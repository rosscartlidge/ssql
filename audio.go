package ssql

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"iter"
	"math"
	"os"
)

// WAVMetadata contains audio file metadata extracted from the WAV header.
type WAVMetadata struct {
	SampleRate    int // Samples per second (e.g., 44100)
	NumChannels   int // 1=mono, 2=stereo
	BitsPerSample int // 8, 16, 24, or 32
	AudioFormat   int // 1=PCM, 3=IEEE float
	NumSamples    int // Total number of samples (per channel)
}

// wavHeader represents the RIFF WAVE file format header.
type wavHeader struct {
	ChunkID       [4]byte // "RIFF"
	ChunkSize     uint32
	Format        [4]byte // "WAVE"
	Subchunk1ID   [4]byte // "fmt "
	Subchunk1Size uint32
	AudioFormat   uint16 // 1=PCM, 3=IEEE float
	NumChannels   uint16
	SampleRate    uint32
	ByteRate      uint32
	BlockAlign    uint16
	BitsPerSample uint16
}

// ReadWAV reads a WAV file and returns records with sample/amplitude fields.
// Stereo files are mixed to mono by default (average of channels).
// Returns an iterator of Records with:
//   - sample: int64 - sample index (0, 1, 2, ...)
//   - amplitude: float64 - normalized to [-1.0, 1.0]
//
// Returns error if file cannot be opened or has invalid format.
func ReadWAV(filename string) (iter.Seq[Record], *WAVMetadata, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, nil, fmt.Errorf("opening WAV file: %w", err)
	}

	meta, dataOffset, dataSize, err := parseWAVHeader(file)
	if err != nil {
		file.Close()
		return nil, nil, err
	}

	// Seek to data start
	if _, err := file.Seek(int64(dataOffset), io.SeekStart); err != nil {
		file.Close()
		return nil, nil, fmt.Errorf("seeking to data: %w", err)
	}

	seq := createWAVIterator(file, meta, dataSize, -1) // -1 means mix to mono

	return seq, meta, nil
}

// ReadWAVFromReader reads WAV from an io.Reader.
// The reader must be positioned at the start of the WAV file.
func ReadWAVFromReader(r io.Reader) (iter.Seq[Record], *WAVMetadata, error) {
	// For streaming, we need a buffered reader
	br := bufio.NewReader(r)

	meta, _, dataSize, err := parseWAVHeaderFromReader(br)
	if err != nil {
		return nil, nil, err
	}

	seq := createWAVIteratorFromReader(br, meta, dataSize, -1)

	return seq, meta, nil
}

// ReadWAVChannel reads a specific channel from a WAV file.
// channel: 0=left, 1=right (for stereo files)
// For mono files, channel parameter is ignored.
func ReadWAVChannel(filename string, channel int) (iter.Seq[Record], *WAVMetadata, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, nil, fmt.Errorf("opening WAV file: %w", err)
	}

	meta, dataOffset, dataSize, err := parseWAVHeader(file)
	if err != nil {
		file.Close()
		return nil, nil, err
	}

	if channel < 0 || channel >= meta.NumChannels {
		file.Close()
		return nil, nil, fmt.Errorf("channel %d out of range (file has %d channels)", channel, meta.NumChannels)
	}

	// Seek to data start
	if _, err := file.Seek(int64(dataOffset), io.SeekStart); err != nil {
		file.Close()
		return nil, nil, fmt.Errorf("seeking to data: %w", err)
	}

	seq := createWAVIterator(file, meta, dataSize, channel)

	return seq, meta, nil
}

// WriteWAV writes records with "amplitude" field to a WAV file.
// Records must have an "amplitude" field with float64 values in [-1.0, 1.0].
// sampleRate specifies the output sample rate in Hz (e.g., 44100).
func WriteWAV(records iter.Seq[Record], filename string, sampleRate int) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("creating WAV file: %w", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	return WriteWAVToWriter(records, writer, sampleRate)
}

// WriteWAVToWriter writes records with "amplitude" field to an io.Writer.
// Writes 16-bit PCM mono audio.
func WriteWAVToWriter(records iter.Seq[Record], w io.Writer, sampleRate int) error {
	// Collect all samples (we need to know the length for the header)
	var samples []float64
	for r := range records {
		amp := GetOr(r, "amplitude", 0.0)
		samples = append(samples, amp)
	}

	// Calculate sizes
	numSamples := len(samples)
	bitsPerSample := 16
	numChannels := 1
	bytesPerSample := bitsPerSample / 8
	dataSize := numSamples * bytesPerSample
	byteRate := sampleRate * numChannels * bytesPerSample
	blockAlign := numChannels * bytesPerSample

	// Write RIFF header
	if _, err := w.Write([]byte("RIFF")); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(36+dataSize)); err != nil {
		return err
	}
	if _, err := w.Write([]byte("WAVE")); err != nil {
		return err
	}

	// Write fmt chunk
	if _, err := w.Write([]byte("fmt ")); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(16)); err != nil { // fmt chunk size
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint16(1)); err != nil { // PCM format
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint16(numChannels)); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(sampleRate)); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(byteRate)); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint16(blockAlign)); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint16(bitsPerSample)); err != nil {
		return err
	}

	// Write data chunk header
	if _, err := w.Write([]byte("data")); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(dataSize)); err != nil {
		return err
	}

	// Write samples as 16-bit signed integers
	for _, amp := range samples {
		// Clamp to [-1.0, 1.0] and convert to 16-bit signed
		if amp > 1.0 {
			amp = 1.0
		} else if amp < -1.0 {
			amp = -1.0
		}
		sample := int16(amp * 32767)
		if err := binary.Write(w, binary.LittleEndian, sample); err != nil {
			return err
		}
	}

	return nil
}

// ExtractSignalFromWAV directly extracts a Signal from a WAV file.
// This is more efficient than ReadWAV when you just need the signal for FFT/convolution.
// Stereo files are mixed to mono.
func ExtractSignalFromWAV(filename string) (Signal, *WAVMetadata, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, nil, fmt.Errorf("opening WAV file: %w", err)
	}
	defer file.Close()

	meta, dataOffset, _, err := parseWAVHeader(file)
	if err != nil {
		return nil, nil, err
	}

	// Seek to data start
	if _, err := file.Seek(int64(dataOffset), io.SeekStart); err != nil {
		return nil, nil, fmt.Errorf("seeking to data: %w", err)
	}

	// Read all samples directly into Signal
	signal := make(Signal, meta.NumSamples)
	bytesPerSample := meta.BitsPerSample / 8
	buf := make([]byte, bytesPerSample*meta.NumChannels)

	for i := 0; i < meta.NumSamples; i++ {
		if _, err := io.ReadFull(file, buf); err != nil {
			if err == io.EOF {
				signal = signal[:i]
				break
			}
			return nil, nil, fmt.Errorf("reading sample %d: %w", i, err)
		}

		// Mix channels to mono
		var sum float64
		for ch := 0; ch < meta.NumChannels; ch++ {
			offset := ch * bytesPerSample
			val := readSample(buf[offset:offset+bytesPerSample], meta.BitsPerSample, meta.AudioFormat)
			sum += val
		}
		signal[i] = sum / float64(meta.NumChannels)
	}

	return signal, meta, nil
}

// ExtractSignalFromWAVChannel extracts a Signal from a specific channel of a WAV file.
func ExtractSignalFromWAVChannel(filename string, channel int) (Signal, *WAVMetadata, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, nil, fmt.Errorf("opening WAV file: %w", err)
	}
	defer file.Close()

	meta, dataOffset, dataSize, err := parseWAVHeader(file)
	if err != nil {
		return nil, nil, err
	}

	if channel < 0 || channel >= meta.NumChannels {
		return nil, nil, fmt.Errorf("channel %d out of range (file has %d channels)", channel, meta.NumChannels)
	}

	// Seek to data start
	if _, err := file.Seek(int64(dataOffset), io.SeekStart); err != nil {
		return nil, nil, fmt.Errorf("seeking to data: %w", err)
	}

	// Read all samples directly into Signal
	signal := make(Signal, meta.NumSamples)
	bytesPerSample := meta.BitsPerSample / 8
	buf := make([]byte, bytesPerSample*meta.NumChannels)

	for i := 0; i < meta.NumSamples; i++ {
		if _, err := io.ReadFull(file, buf); err != nil {
			if err == io.EOF {
				signal = signal[:i]
				break
			}
			return nil, nil, fmt.Errorf("reading sample %d: %w", i, err)
		}

		offset := channel * bytesPerSample
		signal[i] = readSample(buf[offset:offset+bytesPerSample], meta.BitsPerSample, meta.AudioFormat)
	}

	_ = dataSize // used in earlier validation
	return signal, meta, nil
}

// ============================================================================
// Internal Helper Functions
// ============================================================================

// parseWAVHeader reads and validates WAV file header from a file.
// Returns metadata, data offset, data size, and any error.
func parseWAVHeader(file *os.File) (*WAVMetadata, int, int, error) {
	return parseWAVHeaderFromReaderSeeker(file)
}

// parseWAVHeaderFromReaderSeeker parses header from seekable reader.
func parseWAVHeaderFromReaderSeeker(r io.ReadSeeker) (*WAVMetadata, int, int, error) {
	var header wavHeader

	// Read RIFF chunk
	if err := binary.Read(r, binary.LittleEndian, &header.ChunkID); err != nil {
		return nil, 0, 0, fmt.Errorf("reading RIFF header: %w", err)
	}
	if string(header.ChunkID[:]) != "RIFF" {
		return nil, 0, 0, fmt.Errorf("not a RIFF file: expected 'RIFF', got %q", header.ChunkID)
	}

	if err := binary.Read(r, binary.LittleEndian, &header.ChunkSize); err != nil {
		return nil, 0, 0, fmt.Errorf("reading chunk size: %w", err)
	}

	if err := binary.Read(r, binary.LittleEndian, &header.Format); err != nil {
		return nil, 0, 0, fmt.Errorf("reading format: %w", err)
	}
	if string(header.Format[:]) != "WAVE" {
		return nil, 0, 0, fmt.Errorf("not a WAVE file: expected 'WAVE', got %q", header.Format)
	}

	// Find and read fmt chunk (may need to skip other chunks)
	var dataOffset, dataSize int
	foundFmt := false
	foundData := false

	for !foundData {
		var chunkID [4]byte
		var chunkSize uint32

		if err := binary.Read(r, binary.LittleEndian, &chunkID); err != nil {
			if err == io.EOF && foundFmt {
				break
			}
			return nil, 0, 0, fmt.Errorf("reading chunk ID: %w", err)
		}
		if err := binary.Read(r, binary.LittleEndian, &chunkSize); err != nil {
			return nil, 0, 0, fmt.Errorf("reading chunk size: %w", err)
		}

		switch string(chunkID[:]) {
		case "fmt ":
			// Read format chunk
			if err := binary.Read(r, binary.LittleEndian, &header.AudioFormat); err != nil {
				return nil, 0, 0, fmt.Errorf("reading audio format: %w", err)
			}
			if err := binary.Read(r, binary.LittleEndian, &header.NumChannels); err != nil {
				return nil, 0, 0, fmt.Errorf("reading num channels: %w", err)
			}
			if err := binary.Read(r, binary.LittleEndian, &header.SampleRate); err != nil {
				return nil, 0, 0, fmt.Errorf("reading sample rate: %w", err)
			}
			if err := binary.Read(r, binary.LittleEndian, &header.ByteRate); err != nil {
				return nil, 0, 0, fmt.Errorf("reading byte rate: %w", err)
			}
			if err := binary.Read(r, binary.LittleEndian, &header.BlockAlign); err != nil {
				return nil, 0, 0, fmt.Errorf("reading block align: %w", err)
			}
			if err := binary.Read(r, binary.LittleEndian, &header.BitsPerSample); err != nil {
				return nil, 0, 0, fmt.Errorf("reading bits per sample: %w", err)
			}

			// Skip any extra bytes in fmt chunk
			extraBytes := int(chunkSize) - 16
			if extraBytes > 0 {
				if _, err := r.Seek(int64(extraBytes), io.SeekCurrent); err != nil {
					return nil, 0, 0, fmt.Errorf("skipping extra fmt bytes: %w", err)
				}
			}

			foundFmt = true

		case "data":
			// Get current position as data offset
			pos, err := r.Seek(0, io.SeekCurrent)
			if err != nil {
				return nil, 0, 0, fmt.Errorf("getting data offset: %w", err)
			}
			dataOffset = int(pos)
			dataSize = int(chunkSize)
			foundData = true

		default:
			// Skip unknown chunk
			if _, err := r.Seek(int64(chunkSize), io.SeekCurrent); err != nil {
				return nil, 0, 0, fmt.Errorf("skipping chunk %q: %w", chunkID, err)
			}
		}
	}

	if !foundFmt {
		return nil, 0, 0, fmt.Errorf("no fmt chunk found")
	}

	// Validate format
	if header.AudioFormat != 1 && header.AudioFormat != 3 {
		return nil, 0, 0, fmt.Errorf("unsupported audio format: %d (only PCM=1 and IEEE float=3 are supported)", header.AudioFormat)
	}

	if header.BitsPerSample != 8 && header.BitsPerSample != 16 && header.BitsPerSample != 24 && header.BitsPerSample != 32 {
		return nil, 0, 0, fmt.Errorf("unsupported bits per sample: %d (only 8, 16, 24, 32 are supported)", header.BitsPerSample)
	}

	bytesPerSample := int(header.BitsPerSample) / 8
	numSamples := dataSize / (bytesPerSample * int(header.NumChannels))

	meta := &WAVMetadata{
		SampleRate:    int(header.SampleRate),
		NumChannels:   int(header.NumChannels),
		BitsPerSample: int(header.BitsPerSample),
		AudioFormat:   int(header.AudioFormat),
		NumSamples:    numSamples,
	}

	return meta, dataOffset, dataSize, nil
}

// parseWAVHeaderFromReader parses header from non-seekable reader.
// This is less efficient but works with stdin and other streams.
func parseWAVHeaderFromReader(r io.Reader) (*WAVMetadata, int, int, error) {
	var header wavHeader
	var totalBytesRead int

	// Read RIFF chunk
	if err := binary.Read(r, binary.LittleEndian, &header.ChunkID); err != nil {
		return nil, 0, 0, fmt.Errorf("reading RIFF header: %w", err)
	}
	totalBytesRead += 4
	if string(header.ChunkID[:]) != "RIFF" {
		return nil, 0, 0, fmt.Errorf("not a RIFF file: expected 'RIFF', got %q", header.ChunkID)
	}

	if err := binary.Read(r, binary.LittleEndian, &header.ChunkSize); err != nil {
		return nil, 0, 0, fmt.Errorf("reading chunk size: %w", err)
	}
	totalBytesRead += 4

	if err := binary.Read(r, binary.LittleEndian, &header.Format); err != nil {
		return nil, 0, 0, fmt.Errorf("reading format: %w", err)
	}
	totalBytesRead += 4
	if string(header.Format[:]) != "WAVE" {
		return nil, 0, 0, fmt.Errorf("not a WAVE file: expected 'WAVE', got %q", header.Format)
	}

	// Find and read fmt chunk (may need to skip other chunks)
	var dataOffset, dataSize int
	foundFmt := false
	foundData := false

	for !foundData {
		var chunkID [4]byte
		var chunkSize uint32

		if err := binary.Read(r, binary.LittleEndian, &chunkID); err != nil {
			if err == io.EOF && foundFmt {
				break
			}
			return nil, 0, 0, fmt.Errorf("reading chunk ID: %w", err)
		}
		totalBytesRead += 4
		if err := binary.Read(r, binary.LittleEndian, &chunkSize); err != nil {
			return nil, 0, 0, fmt.Errorf("reading chunk size: %w", err)
		}
		totalBytesRead += 4

		switch string(chunkID[:]) {
		case "fmt ":
			// Read format chunk
			if err := binary.Read(r, binary.LittleEndian, &header.AudioFormat); err != nil {
				return nil, 0, 0, fmt.Errorf("reading audio format: %w", err)
			}
			if err := binary.Read(r, binary.LittleEndian, &header.NumChannels); err != nil {
				return nil, 0, 0, fmt.Errorf("reading num channels: %w", err)
			}
			if err := binary.Read(r, binary.LittleEndian, &header.SampleRate); err != nil {
				return nil, 0, 0, fmt.Errorf("reading sample rate: %w", err)
			}
			if err := binary.Read(r, binary.LittleEndian, &header.ByteRate); err != nil {
				return nil, 0, 0, fmt.Errorf("reading byte rate: %w", err)
			}
			if err := binary.Read(r, binary.LittleEndian, &header.BlockAlign); err != nil {
				return nil, 0, 0, fmt.Errorf("reading block align: %w", err)
			}
			if err := binary.Read(r, binary.LittleEndian, &header.BitsPerSample); err != nil {
				return nil, 0, 0, fmt.Errorf("reading bits per sample: %w", err)
			}
			totalBytesRead += 16

			// Skip any extra bytes in fmt chunk
			extraBytes := int(chunkSize) - 16
			if extraBytes > 0 {
				discardBuf := make([]byte, extraBytes)
				if _, err := io.ReadFull(r, discardBuf); err != nil {
					return nil, 0, 0, fmt.Errorf("skipping extra fmt bytes: %w", err)
				}
				totalBytesRead += extraBytes
			}

			foundFmt = true

		case "data":
			dataOffset = totalBytesRead
			dataSize = int(chunkSize)
			foundData = true

		default:
			// Skip unknown chunk
			discardBuf := make([]byte, chunkSize)
			if _, err := io.ReadFull(r, discardBuf); err != nil {
				return nil, 0, 0, fmt.Errorf("skipping chunk %q: %w", chunkID, err)
			}
			totalBytesRead += int(chunkSize)
		}
	}

	if !foundFmt {
		return nil, 0, 0, fmt.Errorf("no fmt chunk found")
	}

	// Validate format
	if header.AudioFormat != 1 && header.AudioFormat != 3 {
		return nil, 0, 0, fmt.Errorf("unsupported audio format: %d (only PCM=1 and IEEE float=3 are supported)", header.AudioFormat)
	}

	if header.BitsPerSample != 8 && header.BitsPerSample != 16 && header.BitsPerSample != 24 && header.BitsPerSample != 32 {
		return nil, 0, 0, fmt.Errorf("unsupported bits per sample: %d (only 8, 16, 24, 32 are supported)", header.BitsPerSample)
	}

	bytesPerSample := int(header.BitsPerSample) / 8
	numSamples := dataSize / (bytesPerSample * int(header.NumChannels))

	meta := &WAVMetadata{
		SampleRate:    int(header.SampleRate),
		NumChannels:   int(header.NumChannels),
		BitsPerSample: int(header.BitsPerSample),
		AudioFormat:   int(header.AudioFormat),
		NumSamples:    numSamples,
	}

	return meta, dataOffset, dataSize, nil
}

// createWAVIterator creates an iterator that reads samples from a WAV file.
// channel: -1 for mono mix, 0+ for specific channel.
func createWAVIterator(file *os.File, meta *WAVMetadata, dataSize int, channel int) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		defer file.Close()

		bytesPerSample := meta.BitsPerSample / 8
		buf := make([]byte, bytesPerSample*meta.NumChannels)
		sampleIndex := int64(0)

		// Create shared schema for all records
		schema := NewSchema([]string{"amplitude", "sample"})

		for i := 0; i < meta.NumSamples; i++ {
			if _, err := io.ReadFull(file, buf); err != nil {
				if err == io.EOF {
					break
				}
				// Error reading - stop iteration
				return
			}

			var amplitude float64
			if channel < 0 {
				// Mix all channels to mono
				var sum float64
				for ch := 0; ch < meta.NumChannels; ch++ {
					offset := ch * bytesPerSample
					val := readSample(buf[offset:offset+bytesPerSample], meta.BitsPerSample, meta.AudioFormat)
					sum += val
				}
				amplitude = sum / float64(meta.NumChannels)
			} else {
				// Single channel
				offset := channel * bytesPerSample
				amplitude = readSample(buf[offset:offset+bytesPerSample], meta.BitsPerSample, meta.AudioFormat)
			}

			// Create record with shared schema
			values := []any{amplitude, sampleIndex}
			record := NewRecordFromSchema(schema, values)

			if !yield(record) {
				return
			}
			sampleIndex++
		}
	}
}

// createWAVIteratorFromReader creates an iterator that reads from a buffered reader.
func createWAVIteratorFromReader(r *bufio.Reader, meta *WAVMetadata, dataSize int, channel int) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		bytesPerSample := meta.BitsPerSample / 8
		buf := make([]byte, bytesPerSample*meta.NumChannels)
		sampleIndex := int64(0)

		// Create shared schema for all records
		schema := NewSchema([]string{"amplitude", "sample"})

		for i := 0; i < meta.NumSamples; i++ {
			if _, err := io.ReadFull(r, buf); err != nil {
				if err == io.EOF {
					break
				}
				return
			}

			var amplitude float64
			if channel < 0 {
				// Mix all channels to mono
				var sum float64
				for ch := 0; ch < meta.NumChannels; ch++ {
					offset := ch * bytesPerSample
					val := readSample(buf[offset:offset+bytesPerSample], meta.BitsPerSample, meta.AudioFormat)
					sum += val
				}
				amplitude = sum / float64(meta.NumChannels)
			} else {
				// Single channel
				offset := channel * bytesPerSample
				amplitude = readSample(buf[offset:offset+bytesPerSample], meta.BitsPerSample, meta.AudioFormat)
			}

			// Create record with shared schema
			values := []any{amplitude, sampleIndex}
			record := NewRecordFromSchema(schema, values)

			if !yield(record) {
				return
			}
			sampleIndex++
		}
	}
}

// readSample reads a single sample from bytes and normalizes to [-1.0, 1.0].
func readSample(data []byte, bitsPerSample, audioFormat int) float64 {
	switch audioFormat {
	case 3: // IEEE float
		if bitsPerSample == 32 {
			bits := binary.LittleEndian.Uint32(data)
			return float64(math.Float32frombits(bits))
		}
		// 64-bit float (rare but supported)
		if bitsPerSample == 64 {
			bits := binary.LittleEndian.Uint64(data)
			return math.Float64frombits(bits)
		}
	case 1: // PCM
		switch bitsPerSample {
		case 8:
			// 8-bit PCM is unsigned (0-255, center at 128)
			return (float64(data[0]) - 128.0) / 128.0
		case 16:
			sample := int16(binary.LittleEndian.Uint16(data))
			return float64(sample) / 32768.0
		case 24:
			// 24-bit signed (little-endian)
			val := int32(data[0]) | int32(data[1])<<8 | int32(data[2])<<16
			// Sign extend from 24-bit to 32-bit
			if val&0x800000 != 0 {
				val |= -0x1000000
			}
			return float64(val) / 8388608.0
		case 32:
			sample := int32(binary.LittleEndian.Uint32(data))
			return float64(sample) / 2147483648.0
		}
	}
	return 0.0
}
