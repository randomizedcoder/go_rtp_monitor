package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/randomizedcoder/go_rtp_monitor/pkg/go_rtp_generator"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	debugLevelCst = 11

	signalChannelSize = 10

	promListenCst           = ":9902"
	promPathCst             = "/metrics"
	promMaxRequestsInFlight = 10
	promEnableOpenMetrics   = true

	// quantileError    = 0.05
	// summaryVecMaxAge = 5 * time.Minute

	defaultUDPPortCst        = "6666"
	defaultGroupAddressCst   = "232.0.0.1"
	defaultSourceAddresssCst = "10.99.0.1"
	//defaultSourceAddresssCst = "10.10.20.1"
	defaultTTLCst = 5

	defaultPacketsPerSecondCst = 10

	sendDeadlineCst = 500 * time.Millisecond

	rtpVersionCst     = 2
	rtpPayloadTypeCst = 33
	payloadSizeCst    = 1000

	errorFrequencyCst       = 10 * time.Second
	errorDurationSecondsCst = 2
)

var (
	// Passed by "go build -ldflags" for the show version
	commit string
	date   string

	debugLevel int

	seq  uint16
	ssrc uint32

	pC = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: "counters",
			Name:      "genRTP",
			Help:      "genRTP counters",
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

func main() {

	log.Println("go_generate_rtp")

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
	ttl := flag.Int("ttl", defaultTTLCst, "ttl on packets")
	loop := flag.Bool("loop", false, "Multicast loopback")

	pps := flag.Int("pps", defaultPacketsPerSecondCst, "packets per second")

	s := flag.Int("s", 1, "Starting sequence number with zero to generate random")
	ss := flag.Int("ss", 0, "Starting SSRC with zero to generate random")

	ef := flag.Duration("ef", errorFrequencyCst, "Simulated error frequency (duration)")
	ed := flag.Int("ed", errorDurationSecondsCst, "Error duration.  Positive numbers for seconds. Use negative to specify packets.")

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
		log.Println("group:", *group)
		log.Println("source:", *source)
		log.Println("port:", *port)
		log.Println("pps:", *pps)
		log.Println("s:", *s)
		log.Println("ss:", *ss)
		log.Println("ef:", *ef)
		log.Println("ed:", *ed)
	}

	g := go_rtp_generator.NewRTPGenerator(
		*dl,
		uint16(*s),
		uint32(*ss),
		*source,
		*group,
		*port,
		*ttl,
		*loop,
		*pps,
		*ef,
		*ed,
	)

	g.StartSendLoop()

	log.Println("go_generate_rtp: That's all Folks!")
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
