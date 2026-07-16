package service

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func buildPCM16WAV(duration time.Duration, sampleRate int) []byte {
	sampleCount := int(float64(sampleRate) * duration.Seconds())
	dataSize := sampleCount * 2 // mono, signed 16-bit PCM
	body := make([]byte, 44+dataSize)
	copy(body[0:4], "RIFF")
	binary.LittleEndian.PutUint32(body[4:8], uint32(len(body)-8))
	copy(body[8:12], "WAVE")
	copy(body[12:16], "fmt ")
	binary.LittleEndian.PutUint32(body[16:20], 16)
	binary.LittleEndian.PutUint16(body[20:22], 1)
	binary.LittleEndian.PutUint16(body[22:24], 1)
	binary.LittleEndian.PutUint32(body[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(body[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(body[32:34], 2)
	binary.LittleEndian.PutUint16(body[34:36], 16)
	copy(body[36:40], "data")
	binary.LittleEndian.PutUint32(body[40:44], uint32(dataSize))
	return body
}

func TestDetectAudioDurationWAV(t *testing.T) {
	wav := buildPCM16WAV(2*time.Second, 16_000)
	require.Equal(t, 2*time.Second, detectAudioDuration(wav))
}

func TestDetectAudioDurationWAVIgnoresForgedByteRate(t *testing.T) {
	wav := buildPCM16WAV(2*time.Second, 16_000)
	binary.LittleEndian.PutUint32(wav[28:32], math.MaxUint32)
	require.Equal(t, 2*time.Second, detectAudioDuration(wav))
}

func TestDetectAudioDurationRejectsUnknownPayload(t *testing.T) {
	require.Zero(t, detectAudioDuration([]byte("not-audio")))
}

func TestDetectAudioDurationMP4(t *testing.T) {
	mvhdPayload := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhdPayload[12:16], 48_000)
	binary.BigEndian.PutUint32(mvhdPayload[16:20], 48_000*90)
	mvhd := buildMP4Box("mvhd", mvhdPayload)
	moov := buildMP4Box("moov", mvhd)

	require.Equal(t, 90*time.Second, detectAudioDuration(moov))
}

func TestDetectAudioDurationMP4UsesAudioSampleTimeline(t *testing.T) {
	mvhdPayload := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhdPayload[12:16], 1_000)
	binary.BigEndian.PutUint32(mvhdPayload[16:20], 1_000) // Forged one-second movie duration.

	mdhdPayload := make([]byte, 20)
	binary.BigEndian.PutUint32(mdhdPayload[12:16], 48_000)
	binary.BigEndian.PutUint32(mdhdPayload[16:20], 48_000)
	hdlrPayload := make([]byte, 12)
	copy(hdlrPayload[8:12], "soun")
	sttsPayload := make([]byte, 16)
	binary.BigEndian.PutUint32(sttsPayload[4:8], 1)
	binary.BigEndian.PutUint32(sttsPayload[8:12], 90)
	binary.BigEndian.PutUint32(sttsPayload[12:16], 48_000)
	stbl := buildMP4Box("stbl", buildMP4Box("stts", sttsPayload))
	minf := buildMP4Box("minf", stbl)
	mdia := buildMP4Box("mdia", append(append(buildMP4Box("mdhd", mdhdPayload), buildMP4Box("hdlr", hdlrPayload)...), minf...))
	trak := buildMP4Box("trak", mdia)
	moovPayload := append(buildMP4Box("mvhd", mvhdPayload), trak...)

	require.Equal(t, 90*time.Second, detectAudioDuration(buildMP4Box("moov", moovPayload)))
}

func TestDetectAudioDurationFragmentedMP4UsesTrackRuns(t *testing.T) {
	tkhdPayload := make([]byte, 84)
	binary.BigEndian.PutUint32(tkhdPayload[12:16], 1)
	mdhdPayload := make([]byte, 20)
	binary.BigEndian.PutUint32(mdhdPayload[12:16], 48_000)
	hdlrPayload := make([]byte, 12)
	copy(hdlrPayload[8:12], "soun")
	mdia := buildMP4Box("mdia", append(buildMP4Box("mdhd", mdhdPayload), buildMP4Box("hdlr", hdlrPayload)...))
	trak := buildMP4Box("trak", append(buildMP4Box("tkhd", tkhdPayload), mdia...))

	trexPayload := make([]byte, 24)
	binary.BigEndian.PutUint32(trexPayload[4:8], 1)
	binary.BigEndian.PutUint32(trexPayload[12:16], 480)
	mvex := buildMP4Box("mvex", buildMP4Box("trex", trexPayload))
	moov := buildMP4Box("moov", append(trak, mvex...))

	tfhdPayload := make([]byte, 8)
	binary.BigEndian.PutUint32(tfhdPayload[4:8], 1)
	tfdtPayload := make([]byte, 8)
	trunPayload := make([]byte, 8)
	binary.BigEndian.PutUint32(trunPayload[4:8], 100)
	trafPayload := append(buildMP4Box("tfhd", tfhdPayload), buildMP4Box("tfdt", tfdtPayload)...)
	trafPayload = append(trafPayload, buildMP4Box("trun", trunPayload)...)
	moof := buildMP4Box("moof", buildMP4Box("traf", trafPayload))

	require.Equal(t, time.Second, detectAudioDuration(append(moov, moof...)))
}

func TestDetectAudioDurationWebMWithoutDeclaredDuration(t *testing.T) {
	info := buildEBMLElement([]byte{0x15, 0x49, 0xa9, 0x66}, buildEBMLElement([]byte{0x2a, 0xd7, 0xb1}, []byte{0x0f, 0x42, 0x40}))
	trackEntryPayload := append(buildEBMLElement([]byte{0xd7}, []byte{1}), buildEBMLElement([]byte{0x83}, []byte{2})...)
	trackEntryPayload = append(trackEntryPayload, buildEBMLElement([]byte{0x86}, []byte("A_OPUS"))...)
	tracks := buildEBMLElement([]byte{0x16, 0x54, 0xae, 0x6b}, buildEBMLElement([]byte{0xae}, trackEntryPayload))
	cluster1 := buildWebMOpusCluster(0)
	cluster2 := buildWebMOpusCluster(2_000)
	segmentPayload := append(append(append(info, tracks...), cluster1...), cluster2...)
	data := append(buildEBMLElement([]byte{0x1a, 0x45, 0xdf, 0xa3}, nil), buildEBMLElement([]byte{0x18, 0x53, 0x80, 0x67}, segmentPayload)...)

	require.Equal(t, 2020*time.Millisecond, detectAudioDuration(data))
}

func TestDetectAudioDurationWebMMultipleAudioTracksUsesLongestTimeline(t *testing.T) {
	info := buildEBMLElement([]byte{0x15, 0x49, 0xa9, 0x66}, buildEBMLElement([]byte{0x2a, 0xd7, 0xb1}, []byte{0x0f, 0x42, 0x40}))
	tracksPayload := append(buildWebMOpusTrackEntry(1), buildWebMOpusTrackEntry(2)...)
	tracks := buildEBMLElement([]byte{0x16, 0x54, 0xae, 0x6b}, tracksPayload)
	cluster := buildWebMOpusMultiTrackCluster(100)
	segmentPayload := append(append(info, tracks...), cluster...)
	data := append(buildEBMLElement([]byte{0x1a, 0x45, 0xdf, 0xa3}, nil), buildEBMLElement([]byte{0x18, 0x53, 0x80, 0x67}, segmentPayload)...)

	require.Equal(t, 2*time.Second, detectAudioDuration(data))
}

func TestDetectAudioDurationOggOpus(t *testing.T) {
	serial := uint32(7)
	opusHead := make([]byte, 19)
	copy(opusHead, "OpusHead")
	opusHead[8] = 1
	opusHead[9] = 1
	binary.LittleEndian.PutUint16(opusHead[10:12], 312)
	binary.LittleEndian.PutUint32(opusHead[12:16], 48_000)
	first := buildOggPage(t, 0x02, 0, serial, 0, opusHead)
	lastGranule := uint64(312 + 2*48_000)
	last := buildOggPage(t, 0x04, lastGranule, serial, 1, []byte{0x98})

	require.Equal(t, 2*time.Second, detectAudioDuration(append(first, last...)))
}

func TestDetectAudioDurationOggOpusUsesPacketDurationWhenGranuleIsForgedLow(t *testing.T) {
	serial := uint32(7)
	opusHead := make([]byte, 19)
	copy(opusHead, "OpusHead")
	opusHead[8] = 1
	opusHead[9] = 1
	binary.LittleEndian.PutUint16(opusHead[10:12], 312)
	binary.LittleEndian.PutUint32(opusHead[12:16], 48_000)
	first := buildOggPage(t, 0x02, 0, serial, 0, opusHead)
	// The page claims no decoded samples, but the Opus TOC describes a valid
	// 20 ms packet. Packet-derived duration must prevent a zero-cost request.
	last := buildOggPage(t, 0x04, 0, serial, 1, []byte{0x98})

	require.Equal(t, 20*time.Millisecond, detectAudioDuration(append(first, last...)))
}

func TestDetectAudioDurationOggChainedStreamsAddsDurations(t *testing.T) {
	firstHead := buildOggPage(t, 0x02, 0, 7, 0, buildOpusHead())
	firstEnd := buildOggPage(t, 0x04, 312+48_000, 7, 1, []byte{0x98})
	secondHead := buildOggPage(t, 0x02, 0, 8, 0, buildOpusHead())
	secondEnd := buildOggPage(t, 0x04, 312+48_000, 8, 1, []byte{0x98})
	data := append(append(append(firstHead, firstEnd...), secondHead...), secondEnd...)

	require.Equal(t, 2*time.Second, detectAudioDuration(data))
}

func TestDetectAudioDurationOggMultiplexedStreamsUsesLongestTimeline(t *testing.T) {
	firstHead := buildOggPage(t, 0x02, 0, 7, 0, buildOpusHead())
	secondHead := buildOggPage(t, 0x02, 0, 8, 0, buildOpusHead())
	firstEnd := buildOggPage(t, 0x04, 312+48_000, 7, 1, []byte{0x98})
	secondEnd := buildOggPage(t, 0x04, 312+48_000, 8, 1, []byte{0x98})
	data := append(append(append(firstHead, secondHead...), firstEnd...), secondEnd...)

	require.Equal(t, time.Second, detectAudioDuration(data))
}

func TestDetectAudioDurationFLAC(t *testing.T) {
	streamInfo := make([]byte, 34)
	binary.BigEndian.PutUint16(streamInfo[0:2], 4_096)
	binary.BigEndian.PutUint16(streamInfo[2:4], 4_096)
	packed := uint64(48_000)<<44 | uint64(1)<<41 | uint64(15)<<36 | uint64(96_000)
	binary.BigEndian.PutUint64(streamInfo[10:18], packed)
	data := append([]byte("fLaC"), 0x80, 0, 0, 34)
	data = append(data, streamInfo...)

	require.Equal(t, 2*time.Second, detectAudioDuration(data))
}

func buildMP4Box(boxType string, payload []byte) []byte {
	box := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(box[:4], uint32(len(box)))
	copy(box[4:8], boxType)
	copy(box[8:], payload)
	return box
}

func buildEBMLElement(id, payload []byte) []byte {
	element := append([]byte{}, id...)
	element = append(element, encodeEBMLSize(len(payload))...)
	return append(element, payload...)
}

func encodeEBMLSize(size int) []byte {
	for length := 1; length <= 8; length++ {
		limit := uint64(1)<<(7*length) - 2
		if uint64(size) > limit {
			continue
		}
		value := uint64(size) | uint64(1)<<(7*length)
		encoded := make([]byte, length)
		for index := length - 1; index >= 0; index-- {
			encoded[index] = byte(value)
			value >>= 8
		}
		return encoded
	}
	panic("EBML test payload too large")
}

func buildWebMOpusCluster(timecode uint64) []byte {
	timecodeBytes := []byte{byte(timecode)}
	if timecode > math.MaxUint8 {
		timecodeBytes = []byte{byte(timecode >> 8), byte(timecode)}
	}
	clusterPayload := buildEBMLElement([]byte{0xe7}, timecodeBytes)
	// Track 1, relative timecode 0, no lacing, one 20 ms Opus packet.
	block := []byte{0x81, 0, 0, 0, 0x98}
	clusterPayload = append(clusterPayload, buildEBMLElement([]byte{0xa3}, block)...)
	return buildEBMLElement([]byte{0x1f, 0x43, 0xb6, 0x75}, clusterPayload)
}

func buildWebMOpusTrackEntry(trackNumber byte) []byte {
	payload := append(buildEBMLElement([]byte{0xd7}, []byte{trackNumber}), buildEBMLElement([]byte{0x83}, []byte{2})...)
	payload = append(payload, buildEBMLElement([]byte{0x86}, []byte("A_OPUS"))...)
	return buildEBMLElement([]byte{0xae}, payload)
}

func buildWebMOpusMultiTrackCluster(frameCount int) []byte {
	clusterPayload := buildEBMLElement([]byte{0xe7}, []byte{0})
	for frame := 0; frame < frameCount; frame++ {
		relativeTimecode := int16(frame * 20)
		for _, trackNumber := range []byte{1, 2} {
			block := []byte{
				0x80 | trackNumber,
				byte(uint16(relativeTimecode) >> 8),
				byte(relativeTimecode),
				0,
				0x98,
			}
			clusterPayload = append(clusterPayload, buildEBMLElement([]byte{0xa3}, block)...)
		}
	}
	return buildEBMLElement([]byte{0x1f, 0x43, 0xb6, 0x75}, clusterPayload)
}

func buildOpusHead() []byte {
	opusHead := make([]byte, 19)
	copy(opusHead, "OpusHead")
	opusHead[8] = 1
	opusHead[9] = 1
	binary.LittleEndian.PutUint16(opusHead[10:12], 312)
	binary.LittleEndian.PutUint32(opusHead[12:16], 48_000)
	return opusHead
}

func buildOggPage(t *testing.T, headerType byte, granule uint64, serial, sequence uint32, packet []byte) []byte {
	t.Helper()
	require.LessOrEqual(t, len(packet), 255)
	page := make([]byte, 28+len(packet))
	copy(page[:4], "OggS")
	page[4] = 0
	page[5] = headerType
	binary.LittleEndian.PutUint64(page[6:14], granule)
	binary.LittleEndian.PutUint32(page[14:18], serial)
	binary.LittleEndian.PutUint32(page[18:22], sequence)
	page[26] = 1
	page[27] = byte(len(packet))
	copy(page[28:], packet)
	binary.LittleEndian.PutUint32(page[22:26], calculateOggCRC(page))
	return page
}

func calculateOggCRC(page []byte) uint32 {
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
	return crc
}
