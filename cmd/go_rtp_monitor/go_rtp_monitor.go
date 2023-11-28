package main

// example of multicast listen here, but modified
// https://go.dev/play/p/NprsZPHQmj

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/pion/rtp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/net/ipv4"
)

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
	debugLevelCst = 11

	signalChannelSize = 10

	promListenCst           = ":9901"
	promPathCst             = "/metrics"
	promMaxRequestsInFlight = 10
	promEnableOpenMetrics   = true

	quantileError    = 0.05
	summaryVecMaxAge = 5 * time.Minute

	defaultUDPPortCst        = "6666"
	defaultGroupAddressCst   = "232.0.0.1"
	defaultSourceAddresssCst = "10.10.20.1"

	readBytesCst    = 1500
	readDeadlineCst = 10 * time.Second

	intHACK = "gre"

	baseTen   = 10
	bitSize64 = 64
)

var (
	// Passed by "go build -ldflags" for the show version
	commit string
	date   string

	debugLevel int

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

func main() {

	log.Println("main")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go initSignalHandler(cancel)

	version := flag.Bool("version", false, "version")

	// https://pkg.go.dev/net#Listen
	promListen := flag.String("promListen", promListenCst, "Prometheus http listening socket")
	promPath := flag.String("promPath", promPathCst, "Prometheus http path. Default = /metrics")
	// curl -s http://[::1]:9111/metrics 2>&1 | grep -v "#"
	// curl -s http://127.0.0.1:9111/metrics 2>&1 | grep -v "#"

	dl := flag.Int("dl", debugLevelCst, "nasty debugLevel")

	group := flag.String("group", defaultGroupAddressCst, "Multicast group address")
	source := flag.String("source", defaultSourceAddresssCst, "SSM source IP")
	port := flag.String("port", defaultUDPPortCst, "Multicast UDP port")

	log.Println("Parse")
	flag.Parse()

	if *version {
		fmt.Println("commit:", commit, "\tdate(UTC):", date)
		os.Exit(0)
	}

	debugLevel = *dl

	if debugLevel > 10 {
		log.Println("initPromHandler")
	}

	go initPromHandler(ctx, *promPath, *promListen)

	if debugLevel > 10 {
		log.Println("mcastOpening")
	}

	//ifi := &net.Interface{}
	ifi, err2 := net.InterfaceByName(intHACK)
	if err2 != nil {
		log.Fatal(err2)
	}

	p, err := JoinSSM(*group, *source, *port, ifi)
	if err != nil {
		log.Fatal("JoinSSM err", err)
	}

	if debugLevel > 10 {
		log.Println("readLoop")
	}

	readLoop(p)

	lErr := LeaveSSM(*group, *source, *port, ifi, p)
	if lErr != nil {
		log.Fatal("LeaveSSM lErr", lErr)
	}

	p.Close()

	log.Println("main: That's all Folks!")
}

// initSignalHandler sets up signal handling for the process, and
// will call cancel() when recieved
func initSignalHandler(cancel context.CancelFunc) {
	c := make(chan os.Signal, signalChannelSize)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	<-c
	log.Printf("Signal caught, closing application")
	cancel()
	os.Exit(0)
}

// initPromHandler starts the prom handler with error checking
func initPromHandler(ctx context.Context, promPath string, promListen string) {
	// https: //pkg.go.dev/github.com/prometheus/client_golang/prometheus/promhttp?tab=doc#HandlerOpts
	http.Handle(promPath, promhttp.HandlerFor(
		prometheus.DefaultGatherer,
		promhttp.HandlerOpts{
			EnableOpenMetrics:   promEnableOpenMetrics,
			MaxRequestsInFlight: promMaxRequestsInFlight,
		},
	))
	go func() {
		err := http.ListenAndServe(promListen, nil)
		if err != nil {
			log.Fatal("prometheus error", err)
		}
	}()
}

// Borrowed from
// https://github.com/tongxinCode/mping/blob/master/multicast/listener.go

// JoinSSM Join the SSM group
func JoinSSM(group string, sourceAddress string, port string, ifi *net.Interface) (*ipv4.PacketConn, error) {

	// See also
	//https://pkg.go.dev/net#ListenMulticastUDP
	a := net.ParseIP(group)
	i, err := strconv.ParseInt(port, baseTen, bitSize64)
	if err != nil {
		panic(err)
	}
	ipv4Addr := &net.UDPAddr{IP: a, Port: int(i)}
	c, err := net.ListenUDP("udp", ipv4Addr)
	if err != nil {
		return nil, err
	}
	if debugLevel > 100 {
		log.Println("ListenPacket complete")
	}

	p := ipv4.NewPacketConn(c)
	addr, err := net.ResolveUDPAddr("udp", group+":"+port)
	if err != nil {
		return nil, err
	}
	if debugLevel > 100 {
		log.Println("ResolveUDPAddr address complete")
	}

	sourceAddr, err := net.ResolveUDPAddr("udp", sourceAddress+":")
	if err != nil {
		return nil, err
	}
	if debugLevel > 100 {
		log.Println("ResolveUDPAddr sourceAddr complete")
	}

	err = p.JoinSourceSpecificGroup(ifi, addr, sourceAddr)
	if err != nil {
		if debugLevel > 10 {
			log.Println("JoinSourceSpecificGroup err", err)
		}
		return nil, err
	}

	return p, nil
}

// LeaveSSM: Leave the SSM group
func LeaveSSM(address string, sourceAddress string, port string, ifi *net.Interface, conn *ipv4.PacketConn) error {

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

// readLoops reads packets from the socket
// https://pkg.go.dev/golang.org/x/net/ipv4
func readLoop(c *ipv4.PacketConn) {

	log.Printf("readLoop: reading")

	err := c.SetControlMessage(ipv4.FlagTTL|ipv4.FlagSrc|ipv4.FlagDst|ipv4.FlagInterface, true)
	if err != nil {
		panic(err)
	}

	var lastSequenceNumber uint16

	buf := make([]byte, readBytesCst)

	for i := 0; ; i++ {
		pC.WithLabelValues("readloop", "for", "counter").Inc()

		// Could set a ReadDealine here
		// https://pkg.go.dev/golang.org/x/net/ipv4#PacketConn.SetReadDeadline
		err := c.SetReadDeadline(time.Now().Add(readDeadlineCst))
		if err != nil {
			panic(err)
		}

		if debugLevel > 100 {
			log.Printf("blocking read")
		}

		startReadTime := time.Now()
		//n, cm, src, err := c.ReadFrom(buf)
		n, cm, _, err := c.ReadFrom(buf)
		if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
			if debugLevel > 10 {
				log.Print("syscall.Recvfrom timeout after:", readDeadlineCst.String())
			}
			continue
		}
		if err != nil {
			log.Printf("readLoop: ReadFrom: error %v", err)
			break
		}
		since := time.Since(startReadTime)
		pH.WithLabelValues("readloop", "ReadFrom", "counter").Observe(float64(since.Seconds()))
		pC.WithLabelValues("readloop", "n", "counter").Add(float64(n))
		if i == 0 {
			log.Printf("first read time seconds:%f", since.Seconds())
		}

		p := rtp.Packet{}
		errU := p.Unmarshal(buf[:n])
		if errU != nil {
			panic(errU)
		}

		if !cm.Dst.IsMulticast() {
			pC.WithLabelValues("readloop", "not_multicast", "counter").Add(float64(n))
		}

		log.Printf("readLoop: recv %d bytes from %s to %s", n, cm.Src, cm.Dst)

		log.Print("p\n", p, "\n")

		checkSequenceNumber(p.Header.SequenceNumber, lastSequenceNumber)
	}

	log.Printf("readLoop: exiting")
}

func checkSequenceNumber(current uint16, last uint16) {
	if current != last {
		if last != 0 {
			pC.WithLabelValues("readloop", "sequenceUnexpected", "counter").Add(float64(1 * last))
		}
	}
}
