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

	"github.com/randomizedcoder/go_rtp_monitor/pkg/go_rtp_monitor"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	debugLevelCst = 11

	signalChannelSize = 10

	promListenCst           = ":9901"
	promPathCst             = "/metrics"
	promMaxRequestsInFlight = 10
	promEnableOpenMetrics   = true

	defaultUDPPortCst        = "6666"
	defaultGroupAddressCst   = "232.0.0.1"
	defaultSourceAddresssCst = "10.10.20.1"
	interfaceCst             = "gre"

	awCst = 100
	bwCst = 100
	abCst = 100
	bbCst = 100
)

var (
	// Passed by "go build -ldflags" for the show version
	commit string
	date   string

	debugLevel int
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

	intf := flag.String("intf", interfaceCst, "Network interface to recieve on")
	source := flag.String("source", defaultSourceAddresssCst, "SSM source IP")
	group := flag.String("group", defaultGroupAddressCst, "Multicast group address")
	port := flag.String("port", defaultUDPPortCst, "Multicast UDP port")

	aw := flag.Int("aw", awCst, "ahead window")
	bw := flag.Int("bw", bwCst, "behind window")
	ab := flag.Int("ab", abCst, "ahead buffer")
	bb := flag.Int("bb", bbCst, "behind buffer")

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

	m := go_rtp_monitor.NewRTPMonitor(
		*dl,
		*intf, *source, *group, *port,
		uint16(*aw), uint16(*bw), uint16(*ab), uint16(*bb),
	)

	if debugLevel > 10 {
		log.Println("starting to m.Monitor()")
	}

	m.Monitor()

	defer m.Close()

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
