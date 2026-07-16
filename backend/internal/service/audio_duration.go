package service

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/hajimehoshi/go-mp3"
)

type audioReadSeeker interface {
	io.Reader
	io.ReaderAt
	io.Seeker
}

// detectAudioDuration is the in-memory test/helper entry point. Production
// uploads use detectAudioDurationReader with a file-backed reader so the
// multipart body is not duplicated in memory.
func detectAudioDuration(data []byte) time.Duration {
	return detectAudioDurationReader(bytes.NewReader(data), int64(len(data)))
}

func detectAudioDurationReader(reader audioReadSeeker, size int64) time.Duration {
	if reader == nil || size <= 0 {
		return 0
	}
	header := make([]byte, minInt64(size, 16))
	if !readAtFull(reader, header, 0) {
		return 0
	}

	switch {
	case len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WAVE":
		return detectWAVDurationReader(reader, size)
	case len(header) >= 4 && string(header[:4]) == "fLaC":
		return detectFLACDurationReader(reader, size)
	case len(header) >= 4 && string(header[:4]) == "OggS":
		return detectOggDurationReader(reader, size)
	case len(header) >= 4 && bytes.Equal(header[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}):
		return detectWebMDurationReader(reader, size)
	case isLikelyMP4Header(header):
		return detectMP4DurationReader(reader, size)
	default:
		return detectMP3DurationReader(reader)
	}
}

func minInt64(value int64, limit int) int {
	if value < int64(limit) {
		return int(value)
	}
	return limit
}

func maxUint64(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}

func readAtFull(reader io.ReaderAt, buf []byte, offset int64) bool {
	if len(buf) == 0 {
		return true
	}
	n, err := reader.ReadAt(buf, offset)
	return n == len(buf) && (err == nil || err == io.EOF)
}

func durationFromSamples(samples uint64, sampleRate uint32) time.Duration {
	if samples == 0 || sampleRate == 0 {
		return 0
	}
	return durationFromSeconds(float64(samples) / float64(sampleRate))
}

func detectMP3DurationReader(reader audioReadSeeker) time.Duration {
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return 0
	}
	decoder, err := mp3.NewDecoder(reader)
	if err != nil || decoder.Length() <= 0 || decoder.SampleRate() <= 0 {
		return 0
	}
	// go-mp3 always exposes decoded 16-bit stereo PCM: four bytes per sample.
	return durationFromSamples(uint64(decoder.Length()/4), uint32(decoder.SampleRate()))
}

func detectWAVDurationReader(reader io.ReaderAt, size int64) time.Duration {
	if size < 12 {
		return 0
	}
	var (
		audioFormat   uint16
		channels      uint16
		sampleRate    uint32
		blockAlign    uint16
		bitsPerSample uint16
		dataBytes     uint64
		foundFormat   bool
	)

	for offset := int64(12); offset+8 <= size; {
		header := make([]byte, 8)
		if !readAtFull(reader, header, offset) {
			return 0
		}
		chunkSize := int64(binary.LittleEndian.Uint32(header[4:8]))
		chunkStart := offset + 8
		chunkEnd := chunkStart + chunkSize
		if chunkSize < 0 || chunkEnd < chunkStart || chunkEnd > size {
			return 0
		}

		switch string(header[:4]) {
		case "fmt ":
			if chunkSize < 16 {
				return 0
			}
			format := make([]byte, 16)
			if !readAtFull(reader, format, chunkStart) {
				return 0
			}
			audioFormat = binary.LittleEndian.Uint16(format[0:2])
			channels = binary.LittleEndian.Uint16(format[2:4])
			sampleRate = binary.LittleEndian.Uint32(format[4:8])
			blockAlign = binary.LittleEndian.Uint16(format[12:14])
			bitsPerSample = binary.LittleEndian.Uint16(format[14:16])
			foundFormat = true
		case "data":
			if math.MaxUint64-dataBytes < uint64(chunkSize) {
				return 0
			}
			dataBytes += uint64(chunkSize)
		}

		offset = chunkEnd
		if chunkSize%2 != 0 {
			offset++
		}
	}

	// Only PCM/IEEE-float (including WAVE_FORMAT_EXTENSIBLE) has a duration
	// that can be derived safely from actual data bytes and sample geometry.
	if !foundFormat || (audioFormat != 1 && audioFormat != 3 && audioFormat != 0xfffe) ||
		channels == 0 || sampleRate == 0 || blockAlign == 0 || bitsPerSample == 0 || dataBytes == 0 {
		return 0
	}
	expectedBlockAlign := uint64(channels) * uint64((bitsPerSample+7)/8)
	if expectedBlockAlign == 0 || uint64(blockAlign) != expectedBlockAlign || dataBytes%uint64(blockAlign) != 0 {
		return 0
	}
	return durationFromSamples(dataBytes/uint64(blockAlign), sampleRate)
}

func isLikelyMP4Header(header []byte) bool {
	if len(header) < 8 {
		return false
	}
	switch string(header[4:8]) {
	case "ftyp", "moov", "free", "skip", "wide", "mdat":
		return true
	default:
		return false
	}
}

type mp4Box struct {
	typ          string
	payloadStart int64
	end          int64
}

func walkMP4Boxes(reader io.ReaderAt, start, end int64, visit func(mp4Box) bool) bool {
	for offset := start; offset+8 <= end; {
		header := make([]byte, 16)
		if !readAtFull(reader, header[:8], offset) {
			return false
		}
		boxSize := uint64(binary.BigEndian.Uint32(header[:4]))
		headerSize := int64(8)
		if boxSize == 1 {
			if offset+16 > end || !readAtFull(reader, header, offset) {
				return false
			}
			boxSize = binary.BigEndian.Uint64(header[8:16])
			headerSize = 16
		} else if boxSize == 0 {
			boxSize = uint64(end - offset)
		}
		if boxSize < uint64(headerSize) || boxSize > uint64(end-offset) {
			return false
		}
		boxEnd := offset + int64(boxSize)
		if !visit(mp4Box{typ: string(header[4:8]), payloadStart: offset + headerSize, end: boxEnd}) {
			return true
		}
		offset = boxEnd
	}
	return true
}

func detectMP4DurationReader(reader io.ReaderAt, size int64) time.Duration {
	var movieDuration time.Duration
	var audioDuration time.Duration
	audioTracks := make(map[uint32]mp4AudioTrackInfo)
	defaultSampleDurations := make(map[uint32]uint32)
	fragmentBoxes := make([]mp4Box, 0, 4)
	walkMP4Boxes(reader, 0, size, func(box mp4Box) bool {
		switch box.typ {
		case "moov":
			walkMP4Boxes(reader, box.payloadStart, box.end, func(child mp4Box) bool {
				switch child.typ {
				case "mvhd":
					if duration := parseMP4HeaderDuration(reader, child); duration > movieDuration {
						movieDuration = duration
					}
				case "trak":
					if duration := parseMP4AudioTrackDuration(reader, child); duration > audioDuration {
						audioDuration = duration
					}
					if track, ok := parseMP4AudioTrackInfo(reader, child); ok {
						audioTracks[track.trackID] = track
					}
				case "mvex":
					parseMP4TrackDefaults(reader, child, defaultSampleDurations)
				}
				return true
			})
		case "moof":
			fragmentBoxes = append(fragmentBoxes, box)
		}
		return true
	})

	for trackID, defaultDuration := range defaultSampleDurations {
		track, ok := audioTracks[trackID]
		if !ok {
			continue
		}
		track.defaultSampleDuration = defaultDuration
		audioTracks[trackID] = track
	}
	fragmentEnds := make(map[uint32]uint64)
	for _, fragmentBox := range fragmentBoxes {
		walkMP4Boxes(reader, fragmentBox.payloadStart, fragmentBox.end, func(child mp4Box) bool {
			if child.typ != "traf" {
				return true
			}
			fragment, ok := parseMP4TrackFragment(reader, child, audioTracks)
			if !ok || fragment.duration == 0 {
				return true
			}
			start := fragmentEnds[fragment.trackID]
			if fragment.hasBaseTime {
				start = fragment.baseTime
			}
			if fragment.duration > math.MaxUint64-start {
				return true
			}
			end := start + fragment.duration
			if end > fragmentEnds[fragment.trackID] {
				fragmentEnds[fragment.trackID] = end
			}
			return true
		})
	}
	for trackID, endUnits := range fragmentEnds {
		track := audioTracks[trackID]
		if duration := durationFromSamples(endUnits, track.timescale); duration > audioDuration {
			audioDuration = duration
		}
	}
	if audioDuration > 0 {
		return audioDuration
	}
	return movieDuration
}

func parseMP4HeaderDuration(reader io.ReaderAt, box mp4Box) time.Duration {
	timescale, units := parseMP4HeaderDurationUnits(reader, box)
	if timescale == 0 || units == 0 {
		return 0
	}
	return durationFromSeconds(float64(units) / float64(timescale))
}

func parseMP4HeaderDurationUnits(reader io.ReaderAt, box mp4Box) (uint32, uint64) {
	payloadSize := box.end - box.payloadStart
	if payloadSize < 20 {
		return 0, 0
	}
	buf := make([]byte, minInt64(payloadSize, 32))
	if !readAtFull(reader, buf, box.payloadStart) {
		return 0, 0
	}
	if buf[0] == 1 {
		if len(buf) < 32 {
			return 0, 0
		}
		timescale := binary.BigEndian.Uint32(buf[20:24])
		units := binary.BigEndian.Uint64(buf[24:32])
		if units == math.MaxUint64 {
			return 0, 0
		}
		return timescale, units
	}
	timescale := binary.BigEndian.Uint32(buf[12:16])
	units := binary.BigEndian.Uint32(buf[16:20])
	if units == math.MaxUint32 {
		return 0, 0
	}
	return timescale, uint64(units)
}

func parseMP4AudioTrackDuration(reader io.ReaderAt, track mp4Box) time.Duration {
	var media mp4Box
	walkMP4Boxes(reader, track.payloadStart, track.end, func(box mp4Box) bool {
		if box.typ == "mdia" {
			media = box
			return false
		}
		return true
	})
	if media.end == 0 {
		return 0
	}

	var (
		timescale uint32
		mdhdUnits uint64
		sttsUnits uint64
		handler   string
	)
	walkMP4Boxes(reader, media.payloadStart, media.end, func(box mp4Box) bool {
		switch box.typ {
		case "mdhd":
			timescale, mdhdUnits = parseMP4HeaderDurationUnits(reader, box)
		case "hdlr":
			buf := make([]byte, 12)
			if box.end-box.payloadStart >= int64(len(buf)) && readAtFull(reader, buf, box.payloadStart) {
				handler = string(buf[8:12])
			}
		case "minf":
			sttsUnits = parseMP4SampleDuration(reader, box)
		}
		return true
	})
	if handler != "soun" || timescale == 0 {
		return 0
	}
	units := maxUint64(mdhdUnits, sttsUnits)
	return durationFromSeconds(float64(units) / float64(timescale))
}

func parseMP4SampleDuration(reader io.ReaderAt, minf mp4Box) uint64 {
	var stbl mp4Box
	walkMP4Boxes(reader, minf.payloadStart, minf.end, func(box mp4Box) bool {
		if box.typ == "stbl" {
			stbl = box
			return false
		}
		return true
	})
	if stbl.end == 0 {
		return 0
	}
	var total uint64
	walkMP4Boxes(reader, stbl.payloadStart, stbl.end, func(box mp4Box) bool {
		if box.typ != "stts" || box.end-box.payloadStart < 8 {
			return true
		}
		header := make([]byte, 8)
		if !readAtFull(reader, header, box.payloadStart) {
			return false
		}
		entryCount := uint64(binary.BigEndian.Uint32(header[4:8]))
		if entryCount > uint64((box.end-box.payloadStart-8)/8) {
			return false
		}
		for i := uint64(0); i < entryCount; i++ {
			entry := make([]byte, 8)
			if !readAtFull(reader, entry, box.payloadStart+8+int64(i*8)) {
				return false
			}
			count := uint64(binary.BigEndian.Uint32(entry[:4]))
			delta := uint64(binary.BigEndian.Uint32(entry[4:8]))
			if count != 0 && delta > math.MaxUint64/count {
				return false
			}
			value := count * delta
			if value > math.MaxUint64-total {
				return false
			}
			total += value
		}
		return false
	})
	return total
}

type mp4AudioTrackInfo struct {
	trackID               uint32
	timescale             uint32
	defaultSampleDuration uint32
}

type mp4TrackFragment struct {
	trackID     uint32
	baseTime    uint64
	hasBaseTime bool
	duration    uint64
}

func parseMP4AudioTrackInfo(reader io.ReaderAt, track mp4Box) (mp4AudioTrackInfo, bool) {
	var trackID uint32
	var media mp4Box
	walkMP4Boxes(reader, track.payloadStart, track.end, func(box mp4Box) bool {
		switch box.typ {
		case "tkhd":
			trackID = parseMP4TrackID(reader, box)
		case "mdia":
			media = box
		}
		return true
	})
	if trackID == 0 || media.end == 0 {
		return mp4AudioTrackInfo{}, false
	}

	var timescale uint32
	var handler string
	walkMP4Boxes(reader, media.payloadStart, media.end, func(box mp4Box) bool {
		switch box.typ {
		case "mdhd":
			timescale, _ = parseMP4HeaderDurationUnits(reader, box)
		case "hdlr":
			buf := make([]byte, 12)
			if box.end-box.payloadStart >= int64(len(buf)) && readAtFull(reader, buf, box.payloadStart) {
				handler = string(buf[8:12])
			}
		}
		return true
	})
	if handler != "soun" || timescale == 0 {
		return mp4AudioTrackInfo{}, false
	}
	return mp4AudioTrackInfo{trackID: trackID, timescale: timescale}, true
}

func parseMP4TrackID(reader io.ReaderAt, box mp4Box) uint32 {
	payloadSize := box.end - box.payloadStart
	if payloadSize < 16 {
		return 0
	}
	version := []byte{0}
	if !readAtFull(reader, version, box.payloadStart) {
		return 0
	}
	offset := int64(12)
	if version[0] == 1 {
		offset = 20
	}
	if payloadSize < offset+4 {
		return 0
	}
	buf := make([]byte, 4)
	if !readAtFull(reader, buf, box.payloadStart+offset) {
		return 0
	}
	return binary.BigEndian.Uint32(buf)
}

func parseMP4TrackDefaults(reader io.ReaderAt, mvex mp4Box, defaults map[uint32]uint32) {
	walkMP4Boxes(reader, mvex.payloadStart, mvex.end, func(box mp4Box) bool {
		if box.typ != "trex" || box.end-box.payloadStart < 16 {
			return true
		}
		buf := make([]byte, 16)
		if !readAtFull(reader, buf, box.payloadStart) {
			return true
		}
		trackID := binary.BigEndian.Uint32(buf[4:8])
		defaultDuration := binary.BigEndian.Uint32(buf[12:16])
		if trackID > 0 && defaultDuration > 0 {
			defaults[trackID] = defaultDuration
		}
		return true
	})
}

type mp4TrackFragmentHeader struct {
	trackID               uint32
	defaultSampleDuration uint32
	durationIsEmpty       bool
}

func parseMP4TrackFragment(reader io.ReaderAt, traf mp4Box, tracks map[uint32]mp4AudioTrackInfo) (mp4TrackFragment, bool) {
	var (
		header      mp4TrackFragmentHeader
		headerValid bool
		baseTime    uint64
		hasBaseTime bool
		runs        []mp4Box
	)
	walkMP4Boxes(reader, traf.payloadStart, traf.end, func(box mp4Box) bool {
		switch box.typ {
		case "tfhd":
			header, headerValid = parseMP4TrackFragmentHeader(reader, box)
		case "tfdt":
			baseTime, hasBaseTime = parseMP4BaseDecodeTime(reader, box)
		case "trun":
			runs = append(runs, box)
		}
		return true
	})
	track, ok := tracks[header.trackID]
	if !headerValid || !ok {
		return mp4TrackFragment{}, false
	}
	if header.durationIsEmpty {
		return mp4TrackFragment{trackID: header.trackID, baseTime: baseTime, hasBaseTime: hasBaseTime}, true
	}
	defaultDuration := header.defaultSampleDuration
	if defaultDuration == 0 {
		defaultDuration = track.defaultSampleDuration
	}
	var total uint64
	for _, run := range runs {
		duration, valid := parseMP4TrackRunDuration(reader, run, defaultDuration)
		if !valid || duration > math.MaxUint64-total {
			return mp4TrackFragment{}, false
		}
		total += duration
	}
	return mp4TrackFragment{
		trackID:     header.trackID,
		baseTime:    baseTime,
		hasBaseTime: hasBaseTime,
		duration:    total,
	}, true
}

func parseMP4TrackFragmentHeader(reader io.ReaderAt, box mp4Box) (mp4TrackFragmentHeader, bool) {
	payloadSize := box.end - box.payloadStart
	if payloadSize < 8 {
		return mp4TrackFragmentHeader{}, false
	}
	header := make([]byte, minInt64(payloadSize, 40))
	if !readAtFull(reader, header, box.payloadStart) {
		return mp4TrackFragmentHeader{}, false
	}
	flags := uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
	result := mp4TrackFragmentHeader{
		trackID:         binary.BigEndian.Uint32(header[4:8]),
		durationIsEmpty: flags&0x010000 != 0,
	}
	index := 8
	skip := func(bytes int) bool {
		if index+bytes > len(header) {
			return false
		}
		index += bytes
		return true
	}
	if flags&0x000001 != 0 && !skip(8) { // base-data-offset-present
		return mp4TrackFragmentHeader{}, false
	}
	if flags&0x000002 != 0 && !skip(4) { // sample-description-index-present
		return mp4TrackFragmentHeader{}, false
	}
	if flags&0x000008 != 0 { // default-sample-duration-present
		if index+4 > len(header) {
			return mp4TrackFragmentHeader{}, false
		}
		result.defaultSampleDuration = binary.BigEndian.Uint32(header[index : index+4])
		index += 4
	}
	if flags&0x000010 != 0 && !skip(4) { // default-sample-size-present
		return mp4TrackFragmentHeader{}, false
	}
	if flags&0x000020 != 0 && !skip(4) { // default-sample-flags-present
		return mp4TrackFragmentHeader{}, false
	}
	return result, result.trackID > 0
}

func parseMP4BaseDecodeTime(reader io.ReaderAt, box mp4Box) (uint64, bool) {
	payloadSize := box.end - box.payloadStart
	if payloadSize < 8 {
		return 0, false
	}
	version := []byte{0}
	if !readAtFull(reader, version, box.payloadStart) {
		return 0, false
	}
	if version[0] == 1 {
		if payloadSize < 12 {
			return 0, false
		}
		buf := make([]byte, 8)
		if !readAtFull(reader, buf, box.payloadStart+4) {
			return 0, false
		}
		return binary.BigEndian.Uint64(buf), true
	}
	buf := make([]byte, 4)
	if !readAtFull(reader, buf, box.payloadStart+4) {
		return 0, false
	}
	return uint64(binary.BigEndian.Uint32(buf)), true
}

func parseMP4TrackRunDuration(reader io.ReaderAt, box mp4Box, defaultDuration uint32) (uint64, bool) {
	payloadSize := box.end - box.payloadStart
	if payloadSize < 8 {
		return 0, false
	}
	header := make([]byte, 8)
	if !readAtFull(reader, header, box.payloadStart) {
		return 0, false
	}
	flags := uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
	sampleCount := uint64(binary.BigEndian.Uint32(header[4:8]))
	offset := box.payloadStart + 8
	if flags&0x000001 != 0 { // data-offset-present
		offset += 4
	}
	if flags&0x000004 != 0 { // first-sample-flags-present
		offset += 4
	}
	if offset > box.end {
		return 0, false
	}
	entrySize := int64(0)
	for _, flag := range []uint32{0x000100, 0x000200, 0x000400, 0x000800} {
		if flags&flag != 0 {
			entrySize += 4
		}
	}
	if entrySize > 0 && sampleCount > uint64((box.end-offset)/entrySize) {
		return 0, false
	}
	if flags&0x000100 == 0 { // sample-duration-present
		if defaultDuration == 0 || (sampleCount > 0 && uint64(defaultDuration) > math.MaxUint64/sampleCount) {
			return 0, sampleCount == 0
		}
		return sampleCount * uint64(defaultDuration), true
	}

	var total uint64
	const entriesPerRead = 4096
	for firstSample := uint64(0); firstSample < sampleCount; {
		chunkCount := min(uint64(entriesPerRead), sampleCount-firstSample)
		chunkSize := int64(chunkCount) * entrySize
		buf := make([]byte, chunkSize)
		if !readAtFull(reader, buf, offset+int64(firstSample)*entrySize) {
			return 0, false
		}
		for sample := uint64(0); sample < chunkCount; sample++ {
			entryOffset := int64(sample) * entrySize
			duration := uint64(binary.BigEndian.Uint32(buf[entryOffset : entryOffset+4]))
			if duration > math.MaxUint64-total {
				return 0, false
			}
			total += duration
		}
		firstSample += chunkCount
	}
	return total, true
}

const (
	ebmlIDSegment         = 0x18538067
	ebmlIDInfo            = 0x1549a966
	ebmlIDTracks          = 0x1654ae6b
	ebmlIDTrackEntry      = 0xae
	ebmlIDTrackNumber     = 0xd7
	ebmlIDTrackType       = 0x83
	ebmlIDCodecID         = 0x86
	ebmlIDDefaultDuration = 0x23e383
	ebmlIDTimecodeScale   = 0x2ad7b1
	ebmlIDDuration        = 0x4489
	ebmlIDCluster         = 0x1f43b675
	ebmlIDClusterTimecode = 0xe7
	ebmlIDSimpleBlock     = 0xa3
	ebmlIDBlockGroup      = 0xa0
	ebmlIDBlock           = 0xa1
	ebmlIDBlockDuration   = 0x9b
)

type ebmlElement struct {
	id        uint64
	dataStart int64
	end       int64
	unknown   bool
}

type webMTrackInfo struct {
	trackType       uint64
	codecID         string
	defaultDuration uint64
}

func readEBMLVintAt(reader io.ReaderAt, offset int64, stripMarker bool) (uint64, int, bool, bool) {
	first := []byte{0}
	if !readAtFull(reader, first, offset) || first[0] == 0 {
		return 0, 0, false, false
	}
	length := 1
	marker := byte(0x80)
	for length <= 8 && first[0]&marker == 0 {
		length++
		marker >>= 1
	}
	if length > 8 || (!stripMarker && length > 4) {
		return 0, 0, false, false
	}
	buf := make([]byte, length)
	if !readAtFull(reader, buf, offset) {
		return 0, 0, false, false
	}
	value := uint64(buf[0])
	if stripMarker {
		value = uint64(buf[0] & (marker - 1))
	}
	for i := 1; i < length; i++ {
		value = value<<8 | uint64(buf[i])
	}
	unknown := false
	if stripMarker {
		maxValue := uint64(1)<<(7*length) - 1
		unknown = value == maxValue
	}
	return value, length, unknown, true
}

func readEBMLElement(reader io.ReaderAt, offset, parentEnd int64) (ebmlElement, bool) {
	id, idLen, _, ok := readEBMLVintAt(reader, offset, false)
	if !ok {
		return ebmlElement{}, false
	}
	size, sizeLen, unknown, ok := readEBMLVintAt(reader, offset+int64(idLen), true)
	if !ok {
		return ebmlElement{}, false
	}
	dataStart := offset + int64(idLen+sizeLen)
	end := parentEnd
	if !unknown {
		if size > uint64(parentEnd-dataStart) {
			return ebmlElement{}, false
		}
		end = dataStart + int64(size)
	}
	return ebmlElement{id: id, dataStart: dataStart, end: end, unknown: unknown}, true
}

func walkEBMLChildren(reader io.ReaderAt, start, end int64, visit func(ebmlElement) bool) bool {
	for offset := start; offset < end; {
		element, ok := readEBMLElement(reader, offset, end)
		if !ok || element.end <= element.dataStart {
			return false
		}
		if !visit(element) {
			return true
		}
		if element.unknown {
			return true
		}
		offset = element.end
	}
	return true
}

func readEBMLUint(reader io.ReaderAt, element ebmlElement) (uint64, bool) {
	length := element.end - element.dataStart
	if length <= 0 || length > 8 {
		return 0, false
	}
	buf := make([]byte, length)
	if !readAtFull(reader, buf, element.dataStart) {
		return 0, false
	}
	var value uint64
	for _, b := range buf {
		value = value<<8 | uint64(b)
	}
	return value, true
}

func readEBMLFloat(reader io.ReaderAt, element ebmlElement) (float64, bool) {
	length := element.end - element.dataStart
	buf := make([]byte, length)
	if !readAtFull(reader, buf, element.dataStart) {
		return 0, false
	}
	switch length {
	case 4:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(buf))), true
	case 8:
		return math.Float64frombits(binary.BigEndian.Uint64(buf)), true
	default:
		return 0, false
	}
}

func detectWebMDurationReader(reader io.ReaderAt, size int64) time.Duration {
	var segment ebmlElement
	for offset := int64(0); offset < size; {
		element, ok := readEBMLElement(reader, offset, size)
		if !ok {
			return 0
		}
		if element.id == ebmlIDSegment {
			segment = element
			break
		}
		if element.unknown {
			return 0
		}
		offset = element.end
	}
	if segment.end == 0 {
		return 0
	}

	timecodeScale := uint64(1_000_000)
	var declaredDuration float64
	tracks := make(map[uint64]webMTrackInfo)
	clusters := make([]ebmlElement, 0, 8)
	walkEBMLChildren(reader, segment.dataStart, segment.end, func(element ebmlElement) bool {
		switch element.id {
		case ebmlIDInfo:
			walkEBMLChildren(reader, element.dataStart, element.end, func(child ebmlElement) bool {
				switch child.id {
				case ebmlIDTimecodeScale:
					if value, ok := readEBMLUint(reader, child); ok && value > 0 {
						timecodeScale = value
					}
				case ebmlIDDuration:
					if value, ok := readEBMLFloat(reader, child); ok && value > declaredDuration && !math.IsNaN(value) && !math.IsInf(value, 0) {
						declaredDuration = value
					}
				}
				return true
			})
		case ebmlIDTracks:
			parseWebMTracks(reader, element, tracks)
		case ebmlIDCluster:
			clusters = append(clusters, element)
		}
		return true
	})

	maxNanoseconds := declaredDuration * float64(timecodeScale)
	decodedNanosecondsByTrack := make(map[uint64]float64)
	for _, cluster := range clusters {
		end, decodedByTrack := parseWebMClusterDuration(reader, cluster, timecodeScale, tracks)
		if end > maxNanoseconds {
			maxNanoseconds = end
		}
		for trackNumber, decoded := range decodedByTrack {
			decodedNanosecondsByTrack[trackNumber] += decoded
		}
	}
	// Multiple audio tracks share the same presentation timeline. Packet-derived
	// durations are accumulated per track and then compared, rather than summed
	// across tracks, so alternate-language tracks are not billed twice.
	for _, decoded := range decodedNanosecondsByTrack {
		if decoded > maxNanoseconds {
			maxNanoseconds = decoded
		}
	}
	if maxNanoseconds <= 0 || maxNanoseconds > float64(math.MaxInt64) {
		return 0
	}
	return time.Duration(maxNanoseconds)
}

func parseWebMTracks(reader io.ReaderAt, tracksElement ebmlElement, tracks map[uint64]webMTrackInfo) {
	walkEBMLChildren(reader, tracksElement.dataStart, tracksElement.end, func(entry ebmlElement) bool {
		if entry.id != ebmlIDTrackEntry {
			return true
		}
		var number uint64
		var info webMTrackInfo
		walkEBMLChildren(reader, entry.dataStart, entry.end, func(field ebmlElement) bool {
			switch field.id {
			case ebmlIDTrackNumber:
				number, _ = readEBMLUint(reader, field)
			case ebmlIDTrackType:
				info.trackType, _ = readEBMLUint(reader, field)
			case ebmlIDDefaultDuration:
				info.defaultDuration, _ = readEBMLUint(reader, field)
			case ebmlIDCodecID:
				length := field.end - field.dataStart
				if length > 0 && length <= 128 {
					buf := make([]byte, length)
					if readAtFull(reader, buf, field.dataStart) {
						info.codecID = string(buf)
					}
				}
			}
			return true
		})
		if number > 0 && info.trackType == 2 {
			tracks[number] = info
		}
		return true
	})
}

type webMBlock struct {
	element       ebmlElement
	durationTicks uint64
}

func parseWebMClusterDuration(reader io.ReaderAt, cluster ebmlElement, timecodeScale uint64, tracks map[uint64]webMTrackInfo) (float64, map[uint64]float64) {
	var clusterTimecode uint64
	walkEBMLChildren(reader, cluster.dataStart, cluster.end, func(element ebmlElement) bool {
		if element.id == ebmlIDClusterTimecode {
			clusterTimecode, _ = readEBMLUint(reader, element)
			return false
		}
		return true
	})

	maxNanoseconds := float64(0)
	decodedNanosecondsByTrack := make(map[uint64]float64)
	walkEBMLChildren(reader, cluster.dataStart, cluster.end, func(element ebmlElement) bool {
		switch element.id {
		case ebmlIDSimpleBlock:
			trackNumber, end, decoded := parseWebMBlockEnd(reader, webMBlock{element: element}, clusterTimecode, timecodeScale, tracks)
			if end > maxNanoseconds {
				maxNanoseconds = end
			}
			if trackNumber > 0 {
				decodedNanosecondsByTrack[trackNumber] += decoded
			}
		case ebmlIDBlockGroup:
			block := webMBlock{}
			walkEBMLChildren(reader, element.dataStart, element.end, func(child ebmlElement) bool {
				switch child.id {
				case ebmlIDBlock:
					block.element = child
				case ebmlIDBlockDuration:
					block.durationTicks, _ = readEBMLUint(reader, child)
				}
				return true
			})
			if block.element.end > 0 {
				trackNumber, end, decoded := parseWebMBlockEnd(reader, block, clusterTimecode, timecodeScale, tracks)
				if end > maxNanoseconds {
					maxNanoseconds = end
				}
				if trackNumber > 0 {
					decodedNanosecondsByTrack[trackNumber] += decoded
				}
			}
		}
		return true
	})
	return maxNanoseconds, decodedNanosecondsByTrack
}

type byteSpan struct {
	offset int64
	size   int64
}

func parseWebMBlockEnd(reader io.ReaderAt, block webMBlock, clusterTimecode, timecodeScale uint64, tracks map[uint64]webMTrackInfo) (uint64, float64, float64) {
	blockSize := block.element.end - block.element.dataStart
	if blockSize < 4 {
		return 0, 0, 0
	}
	headerSize := minInt64(blockSize, 4096)
	header := make([]byte, headerSize)
	if !readAtFull(reader, header, block.element.dataStart) {
		return 0, 0, 0
	}
	trackNumber, trackLen, ok := parseEBMLVintBytes(header)
	if !ok || trackLen+3 > len(header) {
		return 0, 0, 0
	}
	relativeTimecode := int16(binary.BigEndian.Uint16(header[trackLen : trackLen+2]))
	flags := header[trackLen+2]
	packetStart := block.element.dataStart + int64(trackLen+3)
	packets := parseWebMLacing(header, trackLen+3, packetStart, block.element.end, flags)
	if len(packets) == 0 {
		return 0, 0, 0
	}
	track, ok := tracks[trackNumber]
	if !ok {
		return 0, 0, 0
	}

	durationNanoseconds := float64(block.durationTicks) * float64(timecodeScale)
	defaultDuration := float64(track.defaultDuration) * float64(len(packets))
	if defaultDuration > durationNanoseconds {
		durationNanoseconds = defaultDuration
	}
	if strings.Contains(strings.ToUpper(track.codecID), "OPUS") {
		var opusDuration time.Duration
		for _, packet := range packets {
			if duration := opusPacketDuration(reader, packet); duration > 0 {
				opusDuration += duration
			}
		}
		if float64(opusDuration) > durationNanoseconds {
			durationNanoseconds = float64(opusDuration)
		}
	}

	absoluteTicks := int64(clusterTimecode) + int64(relativeTimecode)
	if absoluteTicks < 0 {
		return trackNumber, 0, durationNanoseconds
	}
	return trackNumber, float64(absoluteTicks)*float64(timecodeScale) + durationNanoseconds, durationNanoseconds
}

func parseEBMLVintBytes(data []byte) (uint64, int, bool) {
	if len(data) == 0 || data[0] == 0 {
		return 0, 0, false
	}
	length := 1
	marker := byte(0x80)
	for length <= 8 && data[0]&marker == 0 {
		length++
		marker >>= 1
	}
	if length > 8 || len(data) < length {
		return 0, 0, false
	}
	value := uint64(data[0] & (marker - 1))
	for i := 1; i < length; i++ {
		value = value<<8 | uint64(data[i])
	}
	return value, length, true
}

func parseWebMLacing(header []byte, index int, payloadStart, blockEnd int64, flags byte) []byteSpan {
	lacing := (flags >> 1) & 0x03
	if lacing == 0 {
		return []byteSpan{{offset: payloadStart, size: blockEnd - payloadStart}}
	}
	if index >= len(header) {
		return nil
	}
	frameCount := int(header[index]) + 1
	index++
	dataStart := payloadStart + 1
	if frameCount <= 0 || frameCount > 256 || dataStart > blockEnd {
		return nil
	}
	sizes := make([]int64, frameCount)
	switch lacing {
	case 1: // Xiph lacing.
		for i := 0; i < frameCount-1; i++ {
			for {
				if index >= len(header) {
					return nil
				}
				value := int64(header[index])
				index++
				dataStart++
				sizes[i] += value
				if value != 255 {
					break
				}
			}
		}
	case 2: // Fixed-size lacing.
		remaining := blockEnd - dataStart
		if remaining < 0 || remaining%int64(frameCount) != 0 {
			return nil
		}
		for i := range sizes {
			sizes[i] = remaining / int64(frameCount)
		}
	case 3: // EBML lacing.
		first, length, ok := parseEBMLVintBytes(header[index:])
		if !ok {
			return nil
		}
		sizes[0] = int64(first)
		index += length
		dataStart += int64(length)
		for i := 1; i < frameCount-1; i++ {
			value, valueLen, ok := parseEBMLVintBytes(header[index:])
			if !ok {
				return nil
			}
			bias := int64((uint64(1) << (7*valueLen - 1)) - 1)
			sizes[i] = sizes[i-1] + int64(value) - bias
			if sizes[i] < 0 {
				return nil
			}
			index += valueLen
			dataStart += int64(valueLen)
		}
	}

	if lacing != 2 {
		used := int64(0)
		for i := 0; i < frameCount-1; i++ {
			used += sizes[i]
		}
		sizes[frameCount-1] = blockEnd - dataStart - used
		if sizes[frameCount-1] < 0 {
			return nil
		}
	}
	packets := make([]byteSpan, 0, frameCount)
	offset := dataStart
	for _, size := range sizes {
		if size < 0 || offset+size > blockEnd {
			return nil
		}
		packets = append(packets, byteSpan{offset: offset, size: size})
		offset += size
	}
	return packets
}

func opusPacketDuration(reader io.ReaderAt, packet byteSpan) time.Duration {
	if packet.size <= 0 {
		return 0
	}
	buf := make([]byte, minInt64(packet.size, 2))
	if !readAtFull(reader, buf, packet.offset) {
		return 0
	}
	return opusPacketDurationBytes(buf)
}

func opusPacketDurationBytes(packet []byte) time.Duration {
	if len(packet) == 0 {
		return 0
	}
	config := packet[0] >> 3
	var frameDuration time.Duration
	switch {
	case config < 12:
		frameDuration = []time.Duration{10, 20, 40, 60}[config&3] * time.Millisecond
	case config < 16:
		frameDuration = []time.Duration{10, 20}[config&1] * time.Millisecond
	default:
		frameDuration = []time.Duration{2500, 5000, 10000, 20000}[config&3] * time.Microsecond
	}
	frameCount := 1
	switch packet[0] & 0x03 {
	case 1, 2:
		frameCount = 2
	case 3:
		if len(packet) < 2 {
			return 0
		}
		frameCount = int(packet[1] & 0x3f)
	}
	duration := time.Duration(frameCount) * frameDuration
	if frameCount <= 0 || duration > 120*time.Millisecond {
		return 0
	}
	return duration
}

type oggCodecInfo struct {
	sampleRate uint32
	preSkip    uint64
	opus       bool
}

type oggStreamSpan struct {
	firstOffset int64
	lastEnd     int64
}

type oggTimedStream struct {
	oggStreamSpan
	duration time.Duration
}

func detectOggDurationReader(reader io.ReaderAt, size int64) time.Duration {
	codecs := make(map[uint32]oggCodecInfo)
	granules := make(map[uint32]uint64)
	pendingPackets := make(map[uint32][]byte)
	decodedDurations := make(map[uint32]time.Duration)
	streamSpans := make(map[uint32]oggStreamSpan)
	for offset := int64(0); offset+27 <= size; {
		header := make([]byte, 27)
		if !readAtFull(reader, header, offset) || string(header[:4]) != "OggS" || header[4] != 0 {
			return 0
		}
		segmentCount := int(header[26])
		segmentTable := make([]byte, segmentCount)
		if !readAtFull(reader, segmentTable, offset+27) {
			return 0
		}
		bodySize := 0
		for _, segment := range segmentTable {
			bodySize += int(segment)
		}
		pageSize := 27 + segmentCount + bodySize
		if pageSize < 27 || offset+int64(pageSize) > size {
			return 0
		}
		page := make([]byte, pageSize)
		if !readAtFull(reader, page, offset) || !validOggPageCRC(page) {
			return 0
		}
		serial := binary.LittleEndian.Uint32(header[14:18])
		span, seen := streamSpans[serial]
		if !seen {
			span.firstOffset = offset
		}
		span.lastEnd = offset + int64(pageSize)
		streamSpans[serial] = span
		granule := binary.LittleEndian.Uint64(header[6:14])
		if granule != math.MaxUint64 && granule > granules[serial] {
			granules[serial] = granule
		}
		bodyOffset := 27 + segmentCount
		pending := pendingPackets[serial]
		for _, segment := range segmentTable {
			length := int(segment)
			if len(pending) < 64*1024 {
				remaining := 64*1024 - len(pending)
				appendLength := min(length, remaining)
				pending = append(pending, page[bodyOffset:bodyOffset+appendLength]...)
			}
			bodyOffset += length
			if segment < 255 {
				codec, known := codecs[serial]
				if !known {
					if parsed, ok := parseOggCodecHeader(pending); ok {
						codecs[serial] = parsed
					}
				} else if codec.opus && !bytes.HasPrefix(pending, []byte("OpusTags")) {
					decodedDurations[serial] += opusPacketDurationBytes(pending)
				}
				pending = nil
			}
		}
		pendingPackets[serial] = pending
		offset += int64(pageSize)
	}

	streamDurations := make(map[uint32]time.Duration, len(codecs))
	for serial, codec := range codecs {
		granule := granules[serial]
		duration := decodedDurations[serial]
		if granule > codec.preSkip && codec.sampleRate > 0 {
			granuleDuration := durationFromSamples(granule-codec.preSkip, codec.sampleRate)
			if granuleDuration > duration {
				duration = granuleDuration
			}
		}
		streamDurations[serial] = duration
	}

	// Select the longest chain of non-overlapping logical streams. Sequential
	// streams form an Ogg chain and their durations add; overlapping page spans
	// are multiplexed alternatives on the same timeline and must not be summed.
	streams := make([]oggTimedStream, 0, len(streamDurations))
	for serial, duration := range streamDurations {
		if duration > 0 {
			streams = append(streams, oggTimedStream{oggStreamSpan: streamSpans[serial], duration: duration})
		}
	}
	sort.Slice(streams, func(left, right int) bool {
		if streams[left].lastEnd == streams[right].lastEnd {
			return streams[left].firstOffset < streams[right].firstOffset
		}
		return streams[left].lastEnd < streams[right].lastEnd
	})
	best := make([]time.Duration, len(streams)+1)
	for index, stream := range streams {
		previousCount := sort.Search(index, func(candidate int) bool {
			return streams[candidate].lastEnd > stream.firstOffset
		})
		if stream.duration > time.Duration(math.MaxInt64)-best[previousCount] {
			return 0
		}
		withStream := best[previousCount] + stream.duration
		best[index+1] = best[index]
		if withStream > best[index+1] {
			best[index+1] = withStream
		}
	}
	return best[len(streams)]
}

func parseOggCodecHeader(packet []byte) (oggCodecInfo, bool) {
	if len(packet) >= 12 && string(packet[:8]) == "OpusHead" {
		return oggCodecInfo{sampleRate: 48_000, preSkip: uint64(binary.LittleEndian.Uint16(packet[10:12])), opus: true}, true
	}
	if len(packet) >= 16 && packet[0] == 1 && string(packet[1:7]) == "vorbis" {
		rate := binary.LittleEndian.Uint32(packet[12:16])
		return oggCodecInfo{sampleRate: rate}, rate > 0
	}
	return oggCodecInfo{}, false
}

func validOggPageCRC(page []byte) bool {
	if len(page) < 27 {
		return false
	}
	want := binary.LittleEndian.Uint32(page[22:26])
	var crc uint32
	for index, value := range page {
		if index >= 22 && index < 26 {
			value = 0
		}
		crc ^= uint32(value) << 24
		for bit := 0; bit < 8; bit++ {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc == want
}

func detectFLACDurationReader(reader io.ReaderAt, size int64) time.Duration {
	if size < 8 {
		return 0
	}
	var (
		sampleRate      uint32
		declaredSamples uint64
		maxBlockSize    uint16
		frameStart      int64
	)
	for offset := int64(4); offset+4 <= size; {
		header := make([]byte, 4)
		if !readAtFull(reader, header, offset) {
			return 0
		}
		last := header[0]&0x80 != 0
		blockType := header[0] & 0x7f
		blockSize := int64(header[1])<<16 | int64(header[2])<<8 | int64(header[3])
		dataStart := offset + 4
		if blockSize < 0 || dataStart+blockSize > size {
			return 0
		}
		if blockType == 0 {
			if blockSize != 34 {
				return 0
			}
			streamInfo := make([]byte, 34)
			if !readAtFull(reader, streamInfo, dataStart) {
				return 0
			}
			maxBlockSize = binary.BigEndian.Uint16(streamInfo[2:4])
			packed := binary.BigEndian.Uint64(streamInfo[10:18])
			sampleRate = uint32(packed >> 44)
			declaredSamples = packed & 0x0000000fffffffff
		}
		offset = dataStart + blockSize
		if last {
			frameStart = offset
			break
		}
	}
	if sampleRate == 0 || frameStart == 0 {
		return 0
	}

	frameSamples := scanFLACFrameSamples(reader, frameStart, size, maxBlockSize)
	samples := frameSamples
	if samples == 0 {
		// STREAMINFO is a fallback for unusual but valid streams whose frame
		// headers could not be scanned. Normal files use the frame-derived count.
		samples = declaredSamples
	}
	return durationFromSamples(samples, sampleRate)
}

func scanFLACFrameSamples(reader io.ReaderAt, start, end int64, maxBlockSize uint16) uint64 {
	const chunkSize = 64 * 1024
	var (
		maxSamples     uint64
		bestSamples    uint64
		expectedNumber uint64
		variable       bool
		started        bool
	)
	for chunkStart := start; chunkStart < end; chunkStart += chunkSize - 32 {
		length := minInt64(end-chunkStart, chunkSize)
		buf := make([]byte, length)
		if !readAtFull(reader, buf, chunkStart) {
			break
		}
		for index := 0; index+6 < len(buf); index++ {
			if buf[index] != 0xff || buf[index+1]&0xfe != 0xf8 {
				continue
			}
			absolute := chunkStart + int64(index)
			frame, ok := parseFLACFrameHeader(reader, absolute, end, maxBlockSize)
			if !ok {
				continue
			}
			if frame.number == 0 {
				if maxSamples > bestSamples {
					bestSamples = maxSamples
				}
				started = true
				variable = frame.variable
				maxSamples = frame.endSample
				if variable {
					expectedNumber = frame.endSample
				} else {
					expectedNumber = 1
				}
				continue
			}
			if !started || frame.variable != variable || frame.number != expectedNumber {
				continue
			}
			maxSamples = frame.endSample
			if variable {
				expectedNumber = frame.endSample
			} else {
				expectedNumber++
			}
		}
		if length < chunkSize {
			break
		}
	}
	return maxUint64(bestSamples, maxSamples)
}

type flacFrameHeader struct {
	number    uint64
	blockSize uint64
	endSample uint64
	variable  bool
}

func parseFLACFrameHeader(reader io.ReaderAt, offset, end int64, maxBlockSize uint16) (flacFrameHeader, bool) {
	buf := make([]byte, minInt64(end-offset, 32))
	if len(buf) < 6 || !readAtFull(reader, buf, offset) || buf[0] != 0xff || buf[1]&0xfe != 0xf8 || buf[1]&0x02 != 0 {
		return flacFrameHeader{}, false
	}
	blockCode := buf[2] >> 4
	sampleRateCode := buf[2] & 0x0f
	sampleSizeCode := (buf[3] >> 1) & 0x07
	if blockCode == 0 || sampleRateCode == 0x0f || sampleSizeCode == 3 || sampleSizeCode == 7 || buf[3]&1 != 0 {
		return flacFrameHeader{}, false
	}
	number, numberLen, ok := parseFLACUTF8Uint(buf[4:])
	if !ok {
		return flacFrameHeader{}, false
	}
	index := 4 + numberLen
	blockSize, consumed, ok := parseFLACBlockSize(blockCode, buf[index:])
	if !ok {
		return flacFrameHeader{}, false
	}
	index += consumed
	switch sampleRateCode {
	case 12:
		index++
	case 13, 14:
		index += 2
	}
	if index >= len(buf) || flacCRC8(buf[:index]) != buf[index] {
		return flacFrameHeader{}, false
	}
	if blockSize == 0 {
		return flacFrameHeader{}, false
	}
	if buf[1]&1 != 0 { // Variable-block strategy: number is the first sample index.
		if number > math.MaxUint64-blockSize {
			return flacFrameHeader{}, false
		}
		return flacFrameHeader{number: number, blockSize: blockSize, endSample: number + blockSize, variable: true}, true
	}
	fixedBlockSize := uint64(blockSize)
	if maxBlockSize > 0 && uint64(maxBlockSize) > fixedBlockSize {
		fixedBlockSize = uint64(maxBlockSize)
	}
	if number > math.MaxUint64/fixedBlockSize {
		return flacFrameHeader{}, false
	}
	base := number * fixedBlockSize
	if base > math.MaxUint64-blockSize {
		return flacFrameHeader{}, false
	}
	return flacFrameHeader{number: number, blockSize: blockSize, endSample: base + blockSize}, true
}

func parseFLACUTF8Uint(data []byte) (uint64, int, bool) {
	if len(data) == 0 {
		return 0, 0, false
	}
	first := data[0]
	if first&0x80 == 0 {
		return uint64(first), 1, true
	}
	length := 0
	mask := byte(0x80)
	for first&mask != 0 {
		length++
		mask >>= 1
	}
	if length < 2 || length > 7 || len(data) < length {
		return 0, 0, false
	}
	value := uint64(first & (mask - 1))
	for i := 1; i < length; i++ {
		if data[i]&0xc0 != 0x80 {
			return 0, 0, false
		}
		value = value<<6 | uint64(data[i]&0x3f)
	}
	return value, length, true
}

func parseFLACBlockSize(code byte, data []byte) (uint64, int, bool) {
	switch code {
	case 1:
		return 192, 0, true
	case 2, 3, 4, 5:
		return uint64(576 << (code - 2)), 0, true
	case 6:
		if len(data) < 1 {
			return 0, 0, false
		}
		return uint64(data[0]) + 1, 1, true
	case 7:
		if len(data) < 2 {
			return 0, 0, false
		}
		return uint64(binary.BigEndian.Uint16(data[:2])) + 1, 2, true
	case 8, 9, 10, 11, 12, 13, 14, 15:
		return uint64(256 << (code - 8)), 0, true
	default:
		return 0, 0, false
	}
}

func flacCRC8(data []byte) byte {
	var crc byte
	for _, value := range data {
		crc ^= value
		for bit := 0; bit < 8; bit++ {
			if crc&0x80 != 0 {
				crc = crc<<1 ^ 0x07
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func durationFromSeconds(seconds float64) time.Duration {
	if seconds <= 0 || math.IsInf(seconds, 0) || math.IsNaN(seconds) || seconds > float64(math.MaxInt64)/float64(time.Second) {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}
