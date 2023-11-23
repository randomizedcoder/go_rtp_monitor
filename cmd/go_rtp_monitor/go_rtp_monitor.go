package main

// example of multicast listen here, but modified
// https://go.dev/play/p/NprsZPHQmj

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/net/ipv4"
)

const (
	debugLevelCst = 11

	signalChannelSize = 10

	promListenCst           = ":9901"
	promPathCst             = "/metrics"
	promMaxRequestsInFlight = 10
	promEnableOpenMetrics   = true

	quantileError    = 0.05
	summaryVecMaxAge = 5 * time.Minute

	defaultUDPPortCst = 6666
	defaultIPAddressCst = "225.0.0.1"
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

	ip := flag.String("ip",defaultIPAddressCst, "Multicast socket ip address")
	port := flag.Int("port",defaultUDPPortCst,"Multicast UDP port")

	log.Println("Parse")
	flag.Parse()

	if *version {
		fmt.Println("commit:", commit, "\tdate(UTC):", date)
		os.Exit(0)
	}

	debugLevel = *dl

	if debugLevel > 11 {
		log.Println("initPromHandler")
	}

	go initPromHandler(ctx, *promPath, *promListen)

	a := net.ParseIP(*ip)
	if a == nil {
		log.Fatal(fmt.Errorf("bad address: '%s'", *ip))
	}

	if debugLevel > 11 {
		log.Println("mcastOpening")
	}
	s,err := mcastOpen(a, *port)
	if err != nil{
		log.Fatal("mcastOpen err",err)
	}

	if debugLevel > 11 {
		log.Println("readLoop")
	}

	readLoop(s)

	s.Close()

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

func mcastOpen(bindAddr net.IP, port int) (*ipv4.PacketConn, error) {

	startTime := time.Now()
	defer func() {
		pH.WithLabelValues("mcastOpen", "start", "complete").Observe(time.Since(startTime).Seconds())
	}()
	pC.WithLabelValues("mcastOpen", "start", "counter").Inc()

	// https://pkg.go.dev/syscall#Socket
	// https://pkg.go.dev/syscall#pkg-constants
	//s, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, syscall.IPPROTO_UDP)
	s, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, syscall.AF_UNSPEC)
	if err != nil {
		log.Fatal(err)
	}
	if err := syscall.SetsockoptInt(s, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		log.Fatal(err)
	}

	// Dave - NOT doing this for now.  I don't think it's required.  Testing....
	// //syscall.SetsockoptInt(s, syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
	// if err := syscall.SetsockoptString(s, syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, ifname); err != nil {
	// 	log.Fatal(err)
	// }

	lsa := syscall.SockaddrInet4{Port: port}
	copy(lsa.Addr[:], bindAddr.To4())

	if err := syscall.Bind(s, &lsa); err != nil {
		syscall.Close(s)
		log.Fatal(err)
	}

	// start hack

	// https://cs.opensource.google/go/go/+/refs/tags/go1.21.4:src/syscall/ztypes_linux_amd64.go;l=216
	// type IPMreq struct {
	// 	Multiaddr [4]byte /* in_addr */
	// 	Interface [4]byte /* in_addr */
	// }

	// type IPMreqn struct {
	// 	Multiaddr [4]byte /* in_addr */
	// 	Address   [4]byte /* in_addr */
	// 	Ifindex   int32
	// }

	// https://github.com/torvalds/linux/blob/9b6de136b5f0158c60844f85286a593cb70fb364/include/uapi/linux/in.h#L178
	// struct ip_mreq  {
	// 	struct in_addr imr_multiaddr;	/* IP multicast address of group */
	// 	struct in_addr imr_interface;	/* local IP address of interface */
	// };

	// struct ip_mreqn {
	// 	struct in_addr	imr_multiaddr;		/* IP multicast address of group */
	// 	struct in_addr	imr_address;		/* local IP address of interface */
	// 	int		imr_ifindex;		/* Interface index */
	// };

	// struct ip_mreq_source {               <---- MISSING FROM GOLANG
	// 	__be32		imr_multiaddr;
	// 	__be32		imr_interface;
	// 	__be32		imr_sourceaddr;
	// };

	// Try this hack.  Use ip_mreqn instead of ip_mreq_source
	// https://pkg.go.dev/syscall#SetsockoptIPMreqn
	//
	// Golang does have "IP_ADD_SOURCE_MEMBERSHIP         = 0x27" ( https://pkg.go.dev/syscall#pkg-constants )

	sourceAddress := []byte{0x01,0x01,0x01,0x01}
	var sa uint32
	binary.LittleEndian.PutUint32(sourceAddress, sa)
	ipMreqSourceHack := &syscall.IPMreqn{
		//imr_multiaddr;
		//imr_interface;       <----- defaults to zero (0) in golang, so this will defer to the kernel, which is what we want
		Ifindex:	int32(sa), // Ifindex is really imr_sourceaddr
	}

	if err := syscall.SetsockoptIPMreqn(s, syscall.IPPROTO_IP , syscall.IP_ADD_SOURCE_MEMBERSHIP, ipMreqSourceHack); err != nil {
		log.Fatal(err)
	}

	// end hack


	f := os.NewFile(uintptr(s), "")
	c, err := net.FilePacketConn(f)
	f.Close()
	if err != nil {
		log.Fatal(err)
	}
	p := ipv4.NewPacketConn(c)

	return p, nil
}

func readLoop(c *ipv4.PacketConn) {

	log.Printf("readLoop: reading")

	buf := make([]byte, 10000)

	for {
		n, cm, _, err1 := c.ReadFrom(buf)
		if err1 != nil {
			log.Printf("readLoop: ReadFrom: error %v", err1)
			break
		}

		var name string

		ifi, err2 := net.InterfaceByIndex(cm.IfIndex)
		if err2 != nil {
			log.Printf("readLoop: unable to solve ifIndex=%d: error: %v", cm.IfIndex, err2)
		}

		if ifi == nil {
			name = "ifname?"
		} else {
			name = ifi.Name
		}

		log.Printf("readLoop: recv %d bytes from %s to %s on %s", n, cm.Src, cm.Dst, name)
	}

	log.Printf("readLoop: exiting")
}