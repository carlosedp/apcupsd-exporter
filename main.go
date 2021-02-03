package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func handler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	target := query.Get("target")
	if len(query["target"]) != 1 || target == "" {
		http.Error(w, "'target' parameter must be specified once", 400)
		return
	}

	port := query.Get("port")
	if len(query["port"]) != 1 || port == "" {
		http.Error(w, "'port' parameter must be specified", 400)
		return
	}

	upsAddr := target + ":" + port
	// upsAddr := flag.String("ups-address", "localhost:3551", "The address of the acupsd daemon to query: hostname:port")
	flag.Parse()

	log.Printf("Connection to UPS at: %s", upsAddr)

	start := time.Now()
	registry := prometheus.NewRegistry()
	collector := collector{ctx: r.Context(), target: upsAddr}
	registry.MustRegister(collector)
	// Delegate http serving to Prometheus client library, which will call collector.Collect.
	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
	duration := time.Since(start).Seconds()
	log.Printf("Finished scrape in %+v seconds", duration)
}

func main() {
	// TODO: Register a port for listening here: https://github.com/prometheus/prometheus/wiki/Default-port-allocations
	addr := flag.String("listen-address", ":8080", "The address to listen on for HTTP requests.")
	flag.Parse()
	log.Printf("Metric listener at: %s", *addr)

	http.Handle("/metrics", promhttp.Handler()) // Normal metrics endpoint for APC-UPSD exporter itself.
	// Endpoint to do SNMP scrapes.
	http.HandleFunc("/apcupsd", func(w http.ResponseWriter, r *http.Request) {
		handler(w, r)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
            <head>
            <title>apcupsd Exporter</title>
            <style>
            label{
            display:inline-block;
            width:75px;
            }
            form label {
            margin: 10px;
            }
            form input {
            margin: 10px;
            }
            </style>
            </head>
            <body>
            <h1>apcupsd Exporter</h1>
            <form action="/apcupsd">
            <label>Target:</label> <input type="text" name="target" placeholder="X.X.X.X" value="1.2.3.4"><br>
            <label>Port:</label> <input type="text" name="port" placeholder="3551" value="3551"><br>
            <input type="submit" value="Submit">
            </form>
            </body>
            </html>`))
	})

	// log.Printf(logger).Log("msg", "Listening on address", "address", *listenAddress)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		// level.Error(logger).Log("msg", "Error starting HTTP server", "err", err)
		os.Exit(1)
	}
}
