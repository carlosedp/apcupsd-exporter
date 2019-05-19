package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// map[VERSION:3.14.10 (13 September 2011) debian MINTIMEL:3 Minutes BATTDATE:2014-10-21 END APC:2016-08-30 17 NUMXFERS:0 NOMPOWER:480 Watts NOMINV:230 Volts FIRMWARE:925.T1 .I USB FW APC:001,036,0923 STATUS:ONLINE BCHARGE:100.0 Percent TONBATT:0 seconds HOSTNAME:beaker.murf.org CABLE:USB Cable TIMELEFT:104.6 Minutes SELFTEST:NO ALARMDEL:30 seconds STATFLAG:0x07000008 Status Flag DATE:2016-08-30 17 UPSMODE:Stand Alone MAXTIME:0 Seconds SENSE:Medium HITRANS:280.0 Volts LASTXFER:Unacceptable line voltage changes XOFFBATT:N/A SERIALNO:3B1443X05291 UPSNAME:backups-950 DRIVER:USB UPS Driver STARTTIME:2016-08-30 16 LOADPCT:5.0 Percent Load Capacity MBATTCHG:5 Percent LOTRANS:155.0 Volts BATTV:13.5 Volts CUMONBATT:0 seconds MODEL:Back-UPS XS 950U LINEV:242.0 Volts NOMBATTV:12.0 Volts

type upsInfo struct {
	status string

	nomPower             float64
	batteryChargePercent float64

	timeOnBattery           time.Duration
	timeLeft                time.Duration
	cumTimeOnBattery        time.Duration
	timeTransferToBattery   time.Time
	timeTransferFromBattery time.Time

	loadPercent float64

	batteryVoltage    float64
	lineVoltage       float64
	nomBatteryVoltage float64
	nomInputVoltage   float64

	hostname     string
	upsName      string
	upsModel     string
	lastTransfer string
	batteryDate  string
	numTransfers float64
}

// See SVN code at https://sourceforge.net/p/apcupsd/svn/HEAD/tree/trunk/src/lib/apcstatus.c#l166 for
// list of statuses.
var statusList = []string{
	"online",
	"trim online",
	"boost",
	"trim",
	"onbatt",
	"overload",
	"lowbatt",
	"replacebatt",
	"nobatt",
	"slave",
	"slavedown",
	"commlost",
	"shutting down",
}

var (
	labels = []string{"hostname", "upsname"}

	status = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "apcups_status",
		Help: "Current status of UPS",
	},
		append(labels, "status", "model", "batterydate"),
	)

	statusNumeric = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "apc_status_numeric",
		Help: "Current status of UPS",
	},
		append(labels, "status", "model", "batterydate"),
	)

	nominalPower = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "apcups_nominal_power_watts",
		Help: "Nominal UPS Power",
	},
		labels,
	)

	batteryChargePercent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "apcups_battery_charge_percent",
		Help: "Percentage Battery Charge",
	},
		labels,
	)

	loadPercent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "apcups_load_percent",
		Help: "Percentage Battery Load",
	},
		labels,
	)

	timeOnBattery = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "apcups_time_on_battery_seconds",
		Help: "Total time on UPS battery",
	},
		labels,
	)

	timeLeft = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "apcups_time_left_seconds",
		Help: "Time on UPS battery",
	},
		labels,
	)

	cumTimeOnBattery = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "apcups_cum_time_on_battery_seconds",
		Help: "Cumululative Time on UPS battery",
	},
		labels,
	)

	batteryVoltage = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "apcups_battery_volts",
		Help: "UPS Battery Voltage",
	},
		labels,
	)

	lineVoltage = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "apcups_line_volts",
		Help: "UPS Line Voltage",
	},
		labels,
	)

	nomBatteryVoltage = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "apcups_nom_battery_volts",
		Help: "UPS Nominal Battery Voltage",
	},
		labels,
	)

	nomInputVoltage = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "apcups_nom_input_volts",
		Help: "UPS Nominal Input Voltage",
	},
		labels,
	)

	collectSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "apcups_collect_time_seconds",
		Help: "Time to collect stats for last poll of UPS network interface",
	},
		labels,
	)

	numTransfers = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "apcups_numtransfers",
		Help: "Number of transfers to battery since apcupsd startup.",
	},
		append(labels, "lasttransfer", "timetransfertobattery", "timetransferfrombattery"),
	)
)

func collectUPSData(upsAddr *string) error {

	gatherStart := time.Now()

	data, err := retrieveData(*upsAddr)
	if err != nil {
		return err
	}

	gatherDuration := time.Now().Sub(gatherStart)

	info, err := transformData(data)
	if err != nil {
		return err
	}
	collectSeconds.WithLabelValues(info.hostname, info.upsName).Set(gatherDuration.Seconds())

	log.Printf("%+v", info)

	for i, stat := range statusList {
		if stat == info.status {
			status.WithLabelValues(info.hostname, info.upsName, stat, info.upsModel, info.batteryDate).Set(1)
			statusNumeric.Reset()
			statusNumeric.WithLabelValues(info.hostname, info.upsName, stat, info.upsModel, info.batteryDate).Set(float64(i))
		} else {
			status.WithLabelValues(info.hostname, info.upsName, stat, info.upsModel, info.batteryDate).Set(0)
		}
	}

	// status.WithLabelValues(info.hostname, info.upsName, info.status, info.upsModel, info.batteryDate).Set(1)

	nominalPower.WithLabelValues(info.hostname, info.upsName).Set(info.nomPower)

	batteryChargePercent.WithLabelValues(info.hostname, info.upsName).Set(info.batteryChargePercent)
	timeOnBattery.WithLabelValues(info.hostname, info.upsName).Set(info.timeOnBattery.Seconds())

	timeLeft.WithLabelValues(info.hostname, info.upsName).Set(info.timeLeft.Seconds())

	cumTimeOnBattery.WithLabelValues(info.hostname, info.upsName).Set(info.cumTimeOnBattery.Seconds())
	loadPercent.WithLabelValues(info.hostname, info.upsName).Set(info.loadPercent)
	batteryVoltage.WithLabelValues(info.hostname, info.upsName).Set(info.batteryVoltage)
	lineVoltage.WithLabelValues(info.hostname, info.upsName).Set(info.lineVoltage)
	nomBatteryVoltage.WithLabelValues(info.hostname, info.upsName).Set(info.nomBatteryVoltage)
	nomInputVoltage.WithLabelValues(info.hostname, info.upsName).Set(info.nomInputVoltage)

	numTransfers.Reset()
	numTransfers.WithLabelValues(info.hostname, info.upsName, info.lastTransfer, info.timeTransferToBattery.Format("2006-01-02 15:04:05 -0700"), info.timeTransferFromBattery.Format("2006-01-02 15:04:05 -0700")).Set(info.numTransfers)

	return nil
}

func transformData(ups map[string]string) (*upsInfo, error) {

	upsInfo := &upsInfo{}

	upsInfo.status = strings.ToLower(ups["STATUS"])

	if nomPower, err := parseUnits(ups["NOMPOWER"]); err != nil {
		return nil, err
	} else {
		upsInfo.nomPower = nomPower
	}

	if chargePercent, err := parseUnits(ups["BCHARGE"]); err != nil {
		return nil, err
	} else {
		upsInfo.batteryChargePercent = chargePercent
	}

	if time, err := parseTime(ups["TONBATT"]); err != nil {
		return nil, err
	} else {
		upsInfo.timeOnBattery = time
	}

	if time, err := parseTime(ups["TIMELEFT"]); err != nil {
		return nil, err
	} else {
		upsInfo.timeLeft = time
	}

	if time, err := parseTime(ups["CUMONBATT"]); err != nil {
		return nil, err
	} else {
		upsInfo.cumTimeOnBattery = time
	}

	if percent, err := parseUnits(ups["LOADPCT"]); err != nil {
		return nil, err
	} else {
		upsInfo.loadPercent = percent
	}

	if volts, err := parseUnits(ups["BATTV"]); err != nil {
		return nil, err
	} else {
		upsInfo.batteryVoltage = volts
	}

	if volts, err := parseUnits(ups["LINEV"]); err != nil {
		return nil, err
	} else {
		upsInfo.lineVoltage = volts
	}

	if volts, err := parseUnits(ups["NOMBATTV"]); err != nil {
		return nil, err
	} else {
		upsInfo.nomBatteryVoltage = volts
	}

	if volts, err := parseUnits(ups["NOMINV"]); err != nil {
		return nil, err
	} else {
		upsInfo.nomInputVoltage = volts
	}

	upsInfo.hostname = ups["HOSTNAME"]
	upsInfo.upsName = ups["UPSNAME"]
	upsInfo.upsModel = ups["MODEL"]
	upsInfo.lastTransfer = ups["LASTXFER"]

	const timeForm = "2006-01-02 15:04:05 -0700"
	t1, _ := time.Parse(timeForm, ups["XONBATT"])

	upsInfo.timeTransferToBattery = t1

	t2, _ := time.Parse(timeForm, ups["XOFFBATT"])
	upsInfo.timeTransferFromBattery = t2

	if xf, err := parseUnits(ups["NUMXFERS"]); err != nil {
		return nil, err
	} else {
		upsInfo.numTransfers = xf
	}
	upsInfo.batteryDate = ups["BATTDATE"]

	return upsInfo, nil
}

// parse time strings like 30 seconds or 1.25 minutes
func parseTime(t string) (time.Duration, error) {
	if t == ""{
		return 0, nil
	}
	chunks := strings.Split(t, " ")
	fmtStr := chunks[0] + string(strings.ToLower(chunks[1])[0])
	return time.ParseDuration(fmtStr)
}

// parse generic units, splitting of units name and converting to float
func parseUnits(v string) (float64, error) {
	if v == ""{
		return 0, nil
	}
	return strconv.ParseFloat(strings.Split(v, " ")[0], 32)
}

func retrieveData(hostPort string) (map[string]string, error) {
	conn, err := net.DialTimeout("tcp", hostPort, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("Unable to connect to remote port: %+v", err)
	}

	if _, err = conn.Write([]byte{0, 6}); err != nil {
		return nil, fmt.Errorf("Error writing command length: %+v", err)
	}

	if _, err = conn.Write([]byte("status")); err != nil {
		return nil, fmt.Errorf("Error writing command data: %+v", err)
	}

	complete := false
	upsData := map[string]string{}

	for !complete {
		sizeBuf := []byte{0, 0}
		var size int16
		if _, err := conn.Read(sizeBuf); err != nil {
			return nil, fmt.Errorf("Error reading size from incoming reader: %+v", err)
		}

		if err = binary.Read(bytes.NewBuffer(sizeBuf), binary.BigEndian, &size); err != nil {
			return nil, fmt.Errorf("Error decoding size in response: %+v", err)
		}

		if size > 0 {
			data := make([]byte, size)
			if _, err = conn.Read(data); err != nil {
				log.Panicf("Error reading size from incoming reader: %+v", err)
			}

			var re = regexp.MustCompile(`(?m)^([A-Z]*)\s*:\s*(.*)`)
			matches := re.FindStringSubmatch(string(data))
			if len(matches) >= 3 {
				upsData[strings.TrimSpace(matches[1])] = strings.TrimSpace(matches[2])
			}
		} else {
			complete = true
		}
	}

	if err = conn.Close(); err != nil {
		log.Panicf("Error closing apcupsd connection: %+v", err)
	}

	return upsData, nil
}

func init() {
	prometheus.MustRegister(status)
	prometheus.MustRegister(statusNumeric)
	prometheus.MustRegister(nominalPower)
	prometheus.MustRegister(batteryChargePercent)
	prometheus.MustRegister(timeOnBattery)
	prometheus.MustRegister(timeLeft)
	prometheus.MustRegister(cumTimeOnBattery)
	prometheus.MustRegister(loadPercent)
	prometheus.MustRegister(batteryVoltage)
	prometheus.MustRegister(lineVoltage)
	prometheus.MustRegister(nomBatteryVoltage)
	prometheus.MustRegister(nomInputVoltage)
	prometheus.MustRegister(collectSeconds)
	prometheus.MustRegister(numTransfers)
}

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

	if err := collectUPSData(&upsAddr); err != nil {
		log.Printf("Error collecting UPS data: %+v", err)
	}

	start := time.Now()
	// Delegate http serving to Prometheus client library, which will call collector.Collect.
	h := promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{
		// Opt into OpenMetrics to support exemplars.
		EnableOpenMetrics: true,
	})
	h.ServeHTTP(w, r)
	duration := time.Since(start).Seconds()
	log.Printf("Finished scrape in %f duration_seconds", duration)

}

func main() {
	// TODO: Register a port for listening here: https://github.com/prometheus/prometheus/wiki/Default-port-allocations
	addr := flag.String("listen-address", ":8080", "The address to listen on for HTTP requests.")
	flag.Parse()
	log.Printf("Metric listener at: %s", *addr)

	http.Handle("/metrics", promhttp.Handler()) // Normal metrics endpoint for SNMP exporter itself.
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
