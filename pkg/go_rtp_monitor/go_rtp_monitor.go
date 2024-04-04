package go_rtp_monitor

import (
	"log"
	"net"
	"strconv"
	"time"

	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/pion/rtp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"golang.org/x/net/ipv4"

	"github.com/randomizedcoder/goTrackRTP"
)

// sudo ip route add 224/4 dev virbr0

// example of multicast listen here, but modified
// https://go.dev/play/p/NprsZPHQmj

// Sketches
// https://github.com/DataDog/sketches-go

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
	baseTen   = 10
	bitSize64 = 64

	sketchRelativeAccuracyCst = 0.01
	sketchUpdateModulo        = 10

	median    = 0.5
	p75       = 0.6
	p90       = 0.9
	medianIdx = 0
	p75Idx    = 1
	p90Idx    = 2

	zero = 0

	readBytesCst    = 1500
	readDeadlineCst = 10 * time.Second

	quantileError    = 0.05
	summaryVecMaxAge = 5 * time.Minute
)

var (
	pC = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: "counters",
			Name:      "rtpMonitor",
			Help:      "rtpMonitor counters",
		},
		[]string{"function", "variable", "type"},
	)
	pH = promauto.NewSummaryVec(
		prometheus.SummaryOpts{
			Subsystem: "histrograms",
			Name:      "rtpMonitor",
			Help:      "rtpMonitor historgrams",
			Objectives: map[float64]float64{
				0.1:  quantileError,
				0.5:  quantileError,
				0.99: quantileError,
			},
			MaxAge: summaryVecMaxAge,
		},
		[]string{"function", "variable", "type"},
	)
)

type rtpMonitor struct {
	debugLevel int
	intf       string
	ifi        *net.Interface
	source     string
	group      string
	port       string

	p *ipv4.PacketConn

	tr *goTrackRTP.Tracker
	tm *goTrackRTP.TrackIntToStringMap
}

// NewRTPMonitor creates the rtpGenerator, and opens the UDP socket
func NewRTPMonitor(
	debugLevel int,
	intf, source, group, port string,
	aw, bw, ab, bb uint16,
) *rtpMonitor {

	m := new(rtpMonitor)
	m.debugLevel = debugLevel

	m.source = source
	m.group = group
	m.port = port

	//ifi := &net.Interface{}
	m.intf = intf
	var errI error
	m.ifi, errI = net.InterfaceByName(m.intf)
	if errI != nil {
		log.Fatal(errI)
	}

	var errJ error
	m.p, errJ = m.joinSSM(group, source, port, m.ifi)
	if errJ != nil {
		log.Fatal("JoinSSM err", errJ)
	}

	if debugLevel > 10 {
		log.Println("multicast socket opened")
	}

	var errN error
	m.tr, errN = goTrackRTP.New(aw, bw, ab, bb, debugLevel)
	if errN != nil {
		log.Fatal("goTrackRTP.New:", errN)
	}

	m.tm = goTrackRTP.NewMaps()

	return m
}

// Borrowed from
// https://github.com/tongxinCode/mping/blob/master/multicast/listener.go

// JoinSSM Join the SSM group
func (m *rtpMonitor) joinSSM(
	group string,
	sourceAddress string,
	port string,
	ifi *net.Interface) (*ipv4.PacketConn, error) {

	// See also
	//https://pkg.go.dev/net#ListenMulticastUDP
	a := net.ParseIP(group)
	i, err := strconv.ParseInt(port, baseTen, bitSize64)
	if err != nil {
		log.Fatal("ParseInt error", err)
	}
	ipv4Addr := &net.UDPAddr{IP: a, Port: int(i)}
	c, err := net.ListenUDP("udp", ipv4Addr)
	if err != nil {
		return nil, err
	}
	if m.debugLevel > 100 {
		log.Println("ListenPacket complete")
	}

	p := ipv4.NewPacketConn(c)
	addr, err := net.ResolveUDPAddr("udp", group+":"+port)
	if err != nil {
		return nil, err
	}
	if m.debugLevel > 100 {
		log.Println("ResolveUDPAddr address complete")
	}

	sourceAddr, err := net.ResolveUDPAddr("udp", sourceAddress+":")
	if err != nil {
		return nil, err
	}
	if m.debugLevel > 100 {
		log.Println("ResolveUDPAddr sourceAddr complete")
	}

	err = p.JoinSourceSpecificGroup(ifi, addr, sourceAddr)
	if err != nil {
		if m.debugLevel > 10 {
			log.Println("JoinSourceSpecificGroup err", err)
		}
		return nil, err
	}

	return p, nil
}

// Close leaves the SSM gorup, and then closes the socket
func (m *rtpMonitor) Close() {
	lErr := m.LeaveSSM(m.group, m.source, m.port, m.ifi, m.p)
	if lErr != nil {
		log.Fatal("Close, LeaveSSM lErr", lErr)
	}

	err := m.p.Close()
	if err != nil {
		log.Fatal("Close, Close err", err)
	}
}

// LeaveSSM: Leave the SSM group
func (m *rtpMonitor) LeaveSSM(address string, sourceAddress string, port string, ifi *net.Interface, conn *ipv4.PacketConn) error {

	addr, err := net.ResolveUDPAddr("udp", address+":"+port)
	if err != nil {
		return err
	}

	sourceAddr, err := net.ResolveUDPAddr("udp", sourceAddress+":")
	if err != nil {
		return err
	}

	err = conn.LeaveSourceSpecificGroup(ifi, addr, sourceAddr)
	if err != nil {
		return err
	}

	return nil
}

// Monitor reads packets from the socket and reports on RTP sequence gaps
// https://pkg.go.dev/golang.org/x/net/ipv4
func (m *rtpMonitor) Monitor() {

	if m.debugLevel > 10 {
		log.Println("Monitor()")
	}

	err := m.p.SetControlMessage(ipv4.FlagTTL|ipv4.FlagSrc|ipv4.FlagDst|ipv4.FlagInterface, true)
	if err != nil {
		log.Fatal("SetControlMessage error", err)
	}

	// var lastConsistentSequenceTime time.Time
	// var lastSequenceNumber uint16
	sketch, err := ddsketch.NewDefaultDDSketch(sketchRelativeAccuracyCst)
	if err != nil {
		log.Fatal("NewDefaultDDSketch error", err)
	}
	qs := []float64{median, p75, p90}
	var quantiles []float64
	var quantilesReady bool

	buf := make([]byte, readBytesCst)

	for i := 0; ; i++ {

		pC.WithLabelValues("readloop", "for", "counter").Inc()
		if m.debugLevel > 100 {
			log.Println("readloop() i:", i)
		}

		//n, recieveTime, since, err := m.readPacket(m.p, &buf)
		n, _, since, err := m.readPacket(m.p, &buf)
		if err != nil {
			pC.WithLabelValues("readloop", "readPacket", "error").Inc()
			if m.debugLevel > 10 {
				log.Println("readPacket error:", err)
			}
			continue
		}

		errA := sketch.Add(float64(since.Seconds()))
		if errA != nil {
			log.Fatal("sketch.Add error", errA)
		}
		pC.WithLabelValues("readloop", "sketch.Add", "counter").Inc()

		if i > 0 && i%sketchUpdateModulo == 0 {
			var errG error
			quantiles, errG = sketch.GetValuesAtQuantiles(qs)
			if errG != nil {
				log.Fatal("sketch.GetValuesAtQuantiles error", errG)
			}
			quantilesReady = true
		}

		if quantilesReady {
			if float64(since.Seconds()) > quantiles[p90Idx] {
				pC.WithLabelValues("readloop", "outsideP90", "counter").Inc()
				if m.debugLevel > 10 {
					log.Println("since.Seconds() > p90:", since.Seconds())
				}
			} else if float64(since.Seconds()) > quantiles[p75Idx] {
				pC.WithLabelValues("readloop", "outsideP75", "counter").Inc()
				if m.debugLevel > 10 {
					log.Println("since.Seconds() > p75:", since.Seconds())
				}
			}
		}

		p := rtp.Packet{}
		errU := p.Unmarshal(buf[:n])
		if errU != nil {
			log.Fatal("Unmarshal error", err)
		}

		if m.debugLevel > 100 {
			log.Print("p\n", p, "\n")
		}

		tax, err := m.tr.PacketArrival(p.Header.SequenceNumber)
		if err != nil {
			log.Println("m.tr.PacketArrival(p.Header.SequenceNumber) err:", err)
		}

		pos, c, sc := m.taxConstIntToString(tax)
		if m.debugLevel > 10 {
			log.Printf("pos:%s, c:%s, sc:%s", pos, c, sc)
		}
		pC.WithLabelValues(pos, c, sc).Inc()

		// if m.inconsistentSequence(p.Header.SequenceNumber, lastSequenceNumber) {
		// 	sinceConsistent := recieveTime.Sub(lastConsistentSequenceTime)
		// 	if m.debugLevel > 10 {
		// 		log.Println("sinceConsistent:", sinceConsistent)
		// 	}
		// }

		// lastSequenceNumber = p.SequenceNumber
		// lastConsistentSequenceTime = recieveTime

	}
}

func (m *rtpMonitor) readPacket(c *ipv4.PacketConn, buf *[]byte) (int, time.Time, time.Duration, error) {

	// https://pkg.go.dev/golang.org/x/net/ipv4#PacketConn.SetReadDeadline
	err := c.SetReadDeadline(time.Now().Add(readDeadlineCst))
	if err != nil {
		log.Fatal("SetReadDeadline error", err)
	}

	if m.debugLevel > 100 {
		log.Printf("readPacket blocking read")
	}

	startReadTime := time.Now()

	//n, cm, src, err := c.ReadFrom(buf)
	n, cm, _, err := c.ReadFrom(*buf)

	if nerr, ok := err.(net.Error); ok && nerr.Timeout() {

		if m.debugLevel > 10 {
			log.Print("syscall.Recvfrom timeout after:", readDeadlineCst.String())
		}
		pC.WithLabelValues("readloop", "timeout", "error").Inc()
		return n, time.Time{}, zero, err

	} else if err != nil {
		pC.WithLabelValues("readloop", "ReadFrom", "error").Inc()
		if m.debugLevel > 10 {
			log.Printf("readLoop: ReadFrom: error %v", err)
		}
		return n, time.Time{}, zero, err

	}

	recieveTime := time.Now()
	since := recieveTime.Sub(startReadTime)

	pH.WithLabelValues("readloop", "ReadFrom", "counter").Observe(float64(since.Seconds()))
	pC.WithLabelValues("readloop", "n", "counter").Add(float64(n))

	if !cm.Dst.IsMulticast() {
		pC.WithLabelValues("readloop", "not_multicast", "counter").Add(float64(n))
	}

	if m.debugLevel > 10 {
		log.Printf("readLoop: recv %d bytes from %s to %s", n, cm.Src, cm.Dst)
	}

	return n, recieveTime, since, nil
}

// func (m *rtpMonitor) inconsistentSequence(current uint16, last uint16) (inconsistent bool) {
// 	if last != 0 {
// 		jump := current - (last + 1)
// 		if jump != 0 {
// 			inconsistent = true
// 			pC.WithLabelValues("readloop", "inconsistentSequence", "counter").Inc()
// 			pC.WithLabelValues("readloop", "sequenceJump", "counter").Add(float64(jump))
// 			if m.debugLevel > 10 {
// 				log.Printf("inconsistentSequence current:%d, last:%d, jump:%d", current, last, jump)
// 			}
// 		}
// 	}
// 	return inconsistent
// }

// taxConstIntToString maps the tax const ints to strings to feed to prometheus counters
func (m *rtpMonitor) taxConstIntToString(tax *goTrackRTP.Taxonomy) (position, category, subcategory string) {

	position = m.tm.PosMap[tax.Position]
	category = m.tm.CatMap[tax.Categroy]
	subcategory = m.tm.SubCatMap[tax.SubCategory]

	return position, category, subcategory
}
