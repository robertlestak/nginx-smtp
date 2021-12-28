package main

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	roundrobin "github.com/hlts2/round-robin"
	log "github.com/sirupsen/logrus"
)

var (
	rr   roundrobin.RoundRobin
	pool []*SMTPServer
)

type SMTPServer struct {
	Host    string
	Port    string
	Healthy bool
}

func HandleAuth(w http.ResponseWriter, r *http.Request) {
	l := log.WithFields(log.Fields{
		"method": r.Method,
		"path":   r.URL.Path,
	})
	/*
		requestDump, err := httputil.DumpRequest(r, true)
		if err != nil {
			l.Info(err)
			return
		}
		l.Infof("Request: %s", string(requestDump))
	*/
	smtpPort := r.Header.Get("X-SMTP-Port")
	if smtpPort == "" {
		l.Info("X-SMTP-Port not set")
		smtpPort = "25"
	}
	l.Info("Handling auth request")
	n := rr.Next()
	l.Infof("Using %s", n.Host)
	hp := strings.Split(n.Host, ":")
	w.Header().Set("Auth-Status", "OK")
	w.Header().Set("Auth-Server", hp[0])
	w.Header().Set("Auth-Port", smtpPort)
	w.WriteHeader(http.StatusOK)
}

func SetRoundRobinPool() error {
	l := log.WithFields(log.Fields{
		"action": "SetRoundRobinPool",
	})
	l.Info("Setting new round-robin")
	var p []*url.URL
	for _, h := range pool {
		if h.Healthy {
			p = append(p, &url.URL{
				Host: h.Host + ":" + h.Port,
			})
		}
	}
	l.Printf("healthy pool=%v", p)
	var err error
	if len(p) == 0 {
		return errors.New("no healthy servers")
	}
	rr, err = roundrobin.New(p)
	if err != nil {
		l.WithFields(log.Fields{
			"action": "SetRoundRobinPool",
			"error":  err,
		}).Error("Failed to create new round-robin")
		return err
	}
	return nil
}

func (s *SMTPServer) healthCheckPort() (bool, error) {
	l := log.WithFields(log.Fields{
		"host": s.Host,
		"port": s.Port,
	})
	l.Info("Performing health check")
	timeout := time.Second
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(s.Host, s.Port), timeout)
	if err != nil {
		l.Errorf("instance unhealthy. error=%v", err)
		s.Healthy = false
		return false, err
	}
	if conn != nil {
		defer conn.Close()
		l.Infof("instance healthy")
		s.Healthy = true
	}
	return true, nil
}

func healthCheckWorker(c <-chan *SMTPServer, res chan<- *SMTPServer) {
	l := log.WithFields(log.Fields{
		"method": "healthCheckWorker",
	})
	l.Info("starting worker")
	for u := range c {
		if u == nil {
			continue
		}
		_, err := u.healthCheckPort()
		if err != nil {
			l.WithFields(log.Fields{
				"host": u.Host,
				"port": u.Port,
			}).Errorf("error checking health: %v", err)
			res <- u
		} else {
			res <- u
		}
	}
	l.Info("worker completed work")
}

func HealthCheck() {
	l := log.WithFields(log.Fields{
		"method": "HealthCheck",
		"pool":   len(pool),
	})
	l.Info("Performing health check")
	c := make(chan *SMTPServer, len(pool))
	res := make(chan *SMTPServer, len(pool))
	l.Info("Starting health check workers")
	for i := 0; i < 10; i++ {
		go healthCheckWorker(c, res)
	}
	for _, h := range pool {
		l.Printf("sending %s", h)
		c <- h
	}
	close(c)
	var np []*SMTPServer
	l.Printf("waiting for workers to finish")
	for i := 0; i < len(pool); i++ {
		u := <-res
		l.Printf("worker %d returned %s", i, u)
		if u != nil {
			np = append(np, u)
		}
	}
	pool = np
	l.Printf("new pool: %v", np)
	rerr := SetRoundRobinPool()
	if rerr != nil {
		l.WithFields(log.Fields{
			"error": rerr,
		}).Error("Failed to set new round-robin")
	}
}

func HealthCheckLoop(interval time.Duration) {
	l := log.WithFields(log.Fields{
		"method": "HealthCheckLoop",
	})
	l.Info("Starting health check loop")
	for {
		HealthCheck()
		time.Sleep(interval)
	}
}

func envToHosts() []*SMTPServer {
	l := log.WithFields(log.Fields{
		"module": "auth-server",
		"action": "envToHosts",
	})
	l.Printf("envToHosts")
	var hs []*SMTPServer
	s := os.Getenv("SERVERS")
	l.Printf("SERVERS=%s", s)
	ss := strings.Split(s, ",")
	for _, s := range ss {
		hp := strings.Split(s, ":")
		hs = append(hs, &SMTPServer{Host: hp[0], Port: hp[1]})
	}
	l.Printf("server_count=%d servers=%v", len(hs), hs)
	return hs
}

func init() {
	l := log.WithFields(log.Fields{
		"module": "main",
	})
	l.Info("Initializing auth-server")
	pool = envToHosts()
	if len(pool) == 0 {
		l.Fatal("No servers defined")
	}
}

func main() {
	l := log.WithFields(log.Fields{
		"module": "auth-server",
	})
	l.Info("Starting auth-server")

	r := mux.NewRouter()
	r.HandleFunc("/nginx-auth", HandleAuth)
	port := ":8080"
	if os.Getenv("PORT") != "" {
		port = ":" + os.Getenv("PORT")
	}
	dur, err := time.ParseDuration(os.Getenv("HEALTH_CHECK_INTERVAL"))
	if err != nil {
		l.WithFields(log.Fields{
			"error": err,
		}).Error("Failed to parse health check interval")
	}
	go HealthCheckLoop(dur)
	l.Info("Listening on " + port)
	http.ListenAndServe(port, r)
}
