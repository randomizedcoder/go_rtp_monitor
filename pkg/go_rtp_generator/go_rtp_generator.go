package go_rtp_generator

import (
	"log"
	"math/rand"
	"net"
	"time"

	"github.com/pion/rtp"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"golang.org/x/net/ipv4"

	cr "crypto/rand"
)

// sudo ip route add 224/4 dev virbr0

// https://pkg.go.dev/math/bits

// https://pkg.go.dev/golang.org/x/net/ipv4#hdr-Multicasting

// Found a package that has RTP, so let's use that
// "github.com/pion/rtp"
// https://github.com/pion/rtp/blob/master/packet.go
//
// https://www.rfc-editor.org/rfc/rfc3550#section-5.1
//
// 5.1 RTP Fixed Header Fields

//    The RTP header has the following format:

//     0                   1                   2                   3
//     0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
//    +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//    |V=2|P|X|  CC   |M|     PT      |       sequence number         |
//    +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//    |                           timestamp                           |
//    +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//    |           synchronization source (SSRC) identifier            |
//    +=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+
//    |            contributing source (CSRC) identifiers             |
//    |                             ....                              |
//    +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+

const (
	rtpVersionCst     = 2
	rtpPayloadTypeCst = 33
	payloadSizeCst    = 1000

	sendDeadlineCst = 500 * time.Millisecond
)

type rtpGenerator struct {
	debugLevel           int
	seq                  uint16
	ssrc                 uint32
	source               string
	group                string
	port                 string
	ttl                  int
	loop                 bool
	pps                  int
	errorFrequency       time.Duration
	errorDurationSeconds int

	c *net.UDPConn
}

var (
	pC = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: "counters",
			Name:      "rtp_generator",
			Help:      "rtp_generator counters",
		},
		[]string{"function", "variable", "type"},
	)
	// pH = promauto.NewSummaryVec(
	// 	prometheus.SummaryOpts{
	// 		Subsystem: "histrograms",
	// 		Name:      "genRTP",
	// 		Help:      "genRTP historgrams",
	// 		Objectives: map[float64]float64{
	// 			0.1:  quantileError,
	// 			0.5:  quantileError,
	// 			0.99: quantileError,
	// 		},
	// 		MaxAge: summaryVecMaxAge,
	// 	},
	// 	[]string{"function", "variable", "type"},
	// )
)

// NewRTPGenerator creates the rtpGenerator, and opens the UDP socket
func NewRTPGenerator(
	debugLevel int,
	seq uint16,
	ssrc uint32,
	source string,
	group string,
	port string,
	ttl int,
	loop bool,
	pps int,
	errorFrequency time.Duration,
	errorDurationSeconds int,
) *rtpGenerator {

	g := new(rtpGenerator)

	g.debugLevel = debugLevel
	g.seq = seq
	g.ssrc = ssrc

	g.source = source
	g.group = group
	g.port = port
	g.ttl = ttl
	g.loop = loop
	g.pps = pps

	g.errorFrequency = errorFrequency
	g.errorDurationSeconds = errorDurationSeconds

	g.seq = uint16(seq)
	if ssrc != 0 {
		g.ssrc = ssrc
	} else {
		// https://pkg.go.dev/math/rand#Uint32
		g.ssrc = rand.Uint32()
	}
	if debugLevel > 10 {
		log.Println("g.seq:", g.seq)
		log.Println("g.ssrc:", g.ssrc)
	}

	var err error
	g.c, err = g.openConnection(source, group, port, ttl, loop)
	if err != nil {
		log.Fatal("openConnection err:", err)
	}

	return g
}

func (g *rtpGenerator) openConnection(source string, group string, port string, ttl int, loop bool) (c *net.UDPConn, err error) {

	gp, err := net.ResolveUDPAddr("udp", group+":"+port)
	if err != nil {
		return nil, err
	}

	s, err := net.ResolveUDPAddr("udp", source+":")
	if err != nil {
		return nil, err
	}

	c, err = net.DialUDP("udp", s, gp)
	if err != nil {
		return nil, err
	}
	if g.debugLevel > 100 {
		log.Println("DialUDP complete")
	}

	// https://pkg.go.dev/golang.org/x/net/ipv4#NewPacketConn

	p := ipv4.NewPacketConn(c)
	if g.debugLevel > 100 {
		log.Println("NewPacketConn complete")
	}
	errS := p.SetMulticastTTL(ttl)
	if errS != nil {
		log.Fatal("SetMulticastTTL error", errS)
	}
	if g.debugLevel > 100 {
		log.Println("SetMulticastTTL complete")
	}

	// https://pkg.go.dev/golang.org/x/net/ipv4#PacketConn.MulticastLoopback
	if loop {
		err = p.SetMulticastLoopback(loop)
		if err != nil {
			return nil, err
		}
		if g.debugLevel > 100 {
			log.Println("SetMulticastLoopback complete")
		}
	}

	return c, nil
}

// https://github.com/pion/rtp/blob/master/packet.go#L19
// Header represents an RTP packet header
// type Header struct {
// 	Version          uint8
// 	Padding          bool
// 	Extension        bool
// 	Marker           bool
// 	PayloadType      uint8
// 	SequenceNumber   uint16
// 	Timestamp        uint32
// 	SSRC             uint32
// 	CSRC             []uint32
// 	ExtensionProfile uint16
// 	Extensions       []Extension

// 	// Deprecated: will be removed in a future version.
// 	PayloadOffset int
// }

// // Packet represents an RTP Packet
// type Packet struct {
// 	Header
// 	Payload     []byte
// 	PaddingSize byte

// 	// Deprecated: will be removed in a future version.
// 	Raw []byte
// }

// Example from ffmpeg RTP playout
// Frame 19: 1372 bytes on wire (10976 bits), 1372 bytes captured (10976 bits)
// Linux cooked capture v1
// Internet Protocol Version 4, Src: 10.10.20.1, Dst: 232.0.0.1
// User Datagram Protocol, Src Port: 35635, Dst Port: 6666
// Real-Time Transport Protocol
//     10.. .... = Version: RFC 1889 Version (2)
//     ..0. .... = Padding: False
//     ...0 .... = Extension: False
//     .... 0000 = Contributing source identifiers count: 0
//     0... .... = Marker: False
//     Payload type: MPEG-II transport streams (33)
//     Sequence number: 5540
//     Timestamp: 3102726330
//     Synchronization Source identifier: 0x41eb655a (1105945946)
// ISO/IEC 13818-1 PID=0x100 CC=8
// [Reassembled in: 46]
// ISO/IEC 13818-1 PID=0x100 CC=9
// [Reassembled in: 46]
// ISO/IEC 13818-1 PID=0x100 CC=10
// [Reassembled in: 46]
// ISO/IEC 13818-1 PID=0x100 CC=11
// [Reassembled in: 46]
// ISO/IEC 13818-1 PID=0x100 CC=12
// [Reassembled in: 46]
// ISO/IEC 13818-1 PID=0x100 CC=13
// [Reassembled in: 46]
// ISO/IEC 13818-1 PID=0x100 CC=14
// [Reassembled in: 46]

// StartSendLoop sends the RTP packets at the packet-per-second rate
// Additionlly, it simulates network losses every frequency
func (g *rtpGenerator) StartSendLoop() {

	randPayload := make([]byte, payloadSizeCst)
	n, err := cr.Read(randPayload)
	if err != nil {
		log.Print("n:", n)
		log.Fatal("Crypto Read error", err)
	}

	ticker := time.NewTicker(g.errorFrequency)
	s, seqJump := errorSleepTime(g.pps, g.errorFrequency, g.errorDurationSeconds)

	for {
		pC.WithLabelValues("SendLoop", "for", "counter").Inc()

		p := g.buildPacket()
		p.Payload = randPayload

		b, errM := p.Marshal()
		if errM != nil {
			log.Fatal("Marshal error", errM)
		}

		err := g.c.SetWriteDeadline(time.Now().Add(sendDeadlineCst))
		if err != nil {
			log.Fatal("SetWriteDeadline error", err)
		}

		n, err := g.c.Write(b)
		if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
			if g.debugLevel > 10 {
				log.Print("c.Write timeout after:", sendDeadlineCst.String())
			}
			pC.WithLabelValues("SendLoop", "WriteTimeout", "error").Inc()
			continue
		}
		if g.debugLevel > 10 {
			log.Print("c.Write n:", n)
		}
		pC.WithLabelValues("SendLoop", "n", "counter").Add(float64(n))

		// time assumes sending takes no time, which isn't true, cos syscalls are slow
		time.Sleep(time.Second / time.Duration(g.pps))

		select {
		case <-ticker.C:
			pC.WithLabelValues("SendLoop", "tick", "counter").Inc()
			if g.debugLevel > 10 {
				log.Printf("tick, sleeping for:%s, and jumping:%d", s.String(), seqJump)
			}
			g.seq = g.seq + uint16(seqJump)
			time.Sleep(s)
		default:
			// no tick
		}
	}
}

// errorSleepTime calcualtes the time to sleep when injecting an error
// Positive numbers it sleeps for this many seconds
// Negative numbers it sleeps for this many packets
func errorSleepTime(pps int, ef time.Duration, ed int) (s time.Duration, seqJump int) {
	if ed > 0 {
		s = time.Second * time.Duration(ed)
		seqJump = int(s.Seconds() * float64(pps))
	} else {
		s = (time.Second / time.Duration(pps)) * time.Duration(-ed)
		seqJump = -ed
	}
	return s, seqJump
}

// buildPacket is a helper function to build the actual RTP packet headers
func (g *rtpGenerator) buildPacket() (p *rtp.Packet) {

	p = &rtp.Packet{}

	p.Header.Version = rtpVersionCst
	p.Header.PayloadType = rtpPayloadTypeCst
	g.seq = g.seq + 1
	p.Header.SequenceNumber = g.seq

	// The RTP packet has 32 bit for time, and I thought we had to shave off 32bits,
	// but we are little endian, so the right most bits are the most signficiant,
	// it just needs to be cast to 32bits and this automagically gets rid of the least
	// significant bits

	// So we do NOT need to do this shaving I was talking about here
	// We are using UnixMicro int64, and then shaving off the least significant 32 bits
	// // mask to keep most signficant bits
	// var mask int64 = 0x0FFFFFFF00000000
	// tmm := tm & mask
	// t32 := tmm >> 32

	// https://pkg.go.dev/time#Time.UnixMilli
	t := time.Now()
	tm := t.UnixMilli()
	t32 := uint32(tm)

	if g.debugLevel > 10 {
		//log.Print("t  :", t.String())
		// https://pkg.go.dev/time#UnixMilli
		log.Print("tmm:", time.UnixMilli(tm).String())
		//log.Print("tmm:", time.UnixMilli(tmm).String())
	}

	if g.debugLevel > 100 {
		log.Printf("buildPacket   t Binary: \t%0.16bb\n", tm)
		//log.Printf("buildPacket  tm Binary:\t%0.16bb\n", tmm)
		log.Printf("buildPacket t32 Binary:\t%0.16bb\n", uint32(t32))
	}

	p.Header.Timestamp = uint32(t32)

	p.SSRC = g.ssrc

	return p
}
