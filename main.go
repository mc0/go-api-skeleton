package main

import (
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	pb "github.com/mc0/go-api-skeleton/proto"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
	"gopkg.in/tylerb/graceful.v1"
	"strings"
)

var (
	namespace     = "base"
	subsystem     = "goapiskeleton"
	grpcPort      = new(int)
	httpPort      = new(int)
	mysqlUsername = new(string)
	mysqlPassword = new(string)
	mysqlHostname = new(string)
	mysqlPort     = new(int)
	mysqlDatabase = new(string)
	errorLockChan chan bool
	db            = new(sql.DB)
	metrics       = struct {
		OpenConnections prometheus.Gauge
		Latency         prometheus.Summary
		ErrorBackoffs   prometheus.Counter
		FailedRequests  prometheus.Counter
		Panics          prometheus.Counter
	}{
		prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "open_connections",
			Help:      "Number of currently open connections.",
		}),
		prometheus.NewSummary(prometheus.SummaryOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "latency",
			Help:      "Request latency.",
		}),
		prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "error_backoffs",
			Help:      "How many times the service has reached an error backoff state.",
		}),
		prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "failed_requests",
			Help:      "Requests which did not complete successfully.",
		}),
		prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "panics",
			Help:      "Requests which paniced.",
		}),
	}
)

// The SkeletonServer that we use for gRPC
type SkeletonServer struct {
}

func init() {
	parseIntEnv(grpcPort, "GRPC_PORT", 24601)
	parseIntEnv(httpPort, "HTTP_PORT", 8080)
	parseEnv(mysqlUsername, "MYSQL_USERNAME", "username")
	parseEnv(mysqlPassword, "MYSQL_PASSWORD", "password")
	parseEnv(mysqlHostname, "MYSQL_HOSTNAME", "mysql")
	parseIntEnv(mysqlPort, "MYSQL_PORT", 3306)
	parseEnv(mysqlDatabase, "MYSQL_DATABASE", "skeleton")

	log.SetFlags(log.Lmicroseconds | log.Lshortfile)

	mysqlArgs := strings.Join([]string{
		"readTimeout=1h",
		"writeTimeout=60s",
		// do not tune the following
		"interpolateParams=true",
		"charset=utf8",
		"collation=utf8_general_ci",
	}, "&")

	var err error
	dsn := fmt.Sprintf(
		"%s:%s@tcp(%s)/%s?%s",
		*mysqlUsername,
		*mysqlPassword,
		net.JoinHostPort(*mysqlHostname, strconv.Itoa(*mysqlPort)),
		*mysqlDatabase,
		mysqlArgs)
	db, err = sql.Open("mysql", dsn)
	if err != nil {
		log.Printf("unable to create mysql db: %s", err)
		os.Exit(1)
	}

	prometheus.MustRegister(
		metrics.OpenConnections,
		metrics.Latency,
		metrics.ErrorBackoffs,
		metrics.FailedRequests,
		metrics.Panics,
	)
}

func parseIntEnv(target *int, name string, defaultValue int) {
	if v := os.Getenv(name); v != "" {
		s, err := strconv.ParseInt(v, 10, 0)
		if err != nil {
			e := err.(*strconv.NumError)
			log.Printf("Invalid integer for %s: %s\n%s\n", name, e.Num, e.Err.Error())
		} else {
			*target = int(s)
			return
		}
	}
	*target = defaultValue
}

func parseEnv(target *string, name, defaultValue string) {
	if v := os.Getenv(name); v != "" {
		*target = v
	} else {
		*target = defaultValue
	}
}

// GetObject serves an RPC call that returns the object details.
func (server *SkeletonServer) GetObject(ctx context.Context, obj *pb.Object) (res *pb.Object, err error) {
	// This deferred function closes over the err return value of the main function, so it will attain
	// the most recent value of the err variable.
	defer (func(start time.Time) {
		metrics.Latency.Observe(time.Since(start).Seconds())
		if err != nil {
			metrics.FailedRequests.Inc()
			log.Printf("GetObject error: Id: %s, %s", obj.Id, err)
		} else if r := recover(); r != nil {
			var ok bool
			err, ok = r.(error)
			if !ok {
				err = fmt.Errorf("pkg: %v", r)
			}
			metrics.Panics.Inc()
			stack := make([]byte, 1<<16)
			runtime.Stack(stack, false)
			log.Printf("GetObject panic: Id: %s, %s\n%s", obj.Id, err, stack)
		}
	})(time.Now())

	query := `
SELECT name
FROM object
WHERE id = ?
`
	err = db.QueryRow(query, obj.GetId()).Scan(&obj.Name)
	if err != nil {
		return obj, err
	}

	return obj, err
}

func main() {
	errChan := make(chan int, 1000)
	errorLockChan = make(chan bool, 1)

	// Weird error backoff mechanism.
	var errorLock sync.RWMutex

	// This lets us have a "lock acquisition timeout" on the requests
	go (func() {
		for {
			errorLock.RLock()
			errorLock.RUnlock()
			errorLockChan <- true
		}
	})()

	// This handles locking and unlocking based on important errors, with an exponential ramp up
	// Somewhat in the vein of a circuit breaker
	go (func() {
		recentErrors := 0
		ticker := time.NewTicker(time.Second * 3)
		lastLock := time.Now()
		resetBackoff := time.NewTicker(time.Minute)
		initialBackoff := time.Second * 3
		backoffTime := initialBackoff
		backoffLimit := 50
		for {
			select {
			case <-errChan:
				recentErrors++
				// TODO: change logic in this conditional to be less opaque
				if recentErrors > backoffLimit {
					lastLock = time.Now()
					errorLock.Lock()
					metrics.ErrorBackoffs.Inc()
					time.Sleep(backoffTime)
					recentErrors = 0
					if backoffTime <= initialBackoff*8 {
						backoffTime *= 2
					} else {
					outer:
						for {
							// Clear the channel in case the issue's stopped
							select {
							case <-errChan:
							default:
								break outer
							}
						}
					}
					errorLock.Unlock()
				}
			case <-ticker.C:
				recentErrors = 0
			case <-resetBackoff.C:
				if time.Since(lastLock) > time.Second*60 {
					backoffTime = initialBackoff
					continue
				}
			}
		}
	})()

	// This waitgroup is for waiting on both servers to gracefully close
	var wg sync.WaitGroup
	wg.Add(2)

	// Spin up json endpoint listener
	go (func() {
		mux := http.NewServeMux()
		promHandler := promhttp.Handler()
		mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
			promHandler.ServeHTTP(w, r)
		})
		// Another magic number: in-flight requests have 15 seconds to complete.
		graceful.Run(fmt.Sprintf(":%d", *httpPort), 15*time.Second, mux)
		wg.Done()
	})()

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *grpcPort))
	if err != nil {
		grpclog.Fatalf("failed to listen: %v", err)
	}
	grpcServer := grpc.NewServer()
	skeleton := &SkeletonServer{}
	pb.RegisterSkeletonServer(grpcServer, skeleton)

	// Listen for SIGTERM and SIGINT to shutdown gracefully
	shutdownSig := make(chan os.Signal, 1)
	signal.Notify(shutdownSig, syscall.SIGINT, syscall.SIGTERM)
	go (func() {
		<-shutdownSig
		grpcServer.GracefulStop()
		wg.Done()
	})()
	grpcServer.Serve(lis)
	wg.Wait()
}
