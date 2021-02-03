package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type collector struct {
	ctx    context.Context
	target string
}

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
)

type metricType struct {
	id        string
	name      string
	valueType prometheus.ValueType
	descr     string
	labels    []string
}

var metrics = [...]metricType{
	{
		id:        "status",
		name:      "apcups_status",
		valueType: prometheus.GaugeValue,
		descr:     "Current status of UPS",
		labels:    append(labels, "status", "model", "batterydate"),
	},
	{
		id:        "statusNumeric",
		name:      "apc_status_numeric",
		valueType: prometheus.GaugeValue,
		descr:     "Current status of UPS",
		labels:    append(labels, "status", "model", "batterydate"),
	},
	{
		id:        "collectSeconds",
		name:      "apcups_collect_time_seconds",
		valueType: prometheus.GaugeValue,
		descr:     "Time to collect stats for last poll of UPS network interface",
		labels:    labels,
	},
	{
		id:        "nominalPower",
		name:      "apcups_nominal_power_watts",
		valueType: prometheus.GaugeValue,
		descr:     "Nominal UPS Power",
		labels:    labels,
	},
	{
		id:        "batteryChargePercent",
		name:      "apcups_battery_charge_percent",
		valueType: prometheus.GaugeValue,
		descr:     "Percentage Battery Charge",
		labels:    labels,
	},
	{
		id:        "loadPercent",
		name:      "apcups_load_percent",
		valueType: prometheus.GaugeValue,
		descr:     "Percentage Battery Load",
		labels:    labels,
	},
	{
		id:        "timeOnBattery",
		name:      "apcups_time_on_battery_seconds",
		valueType: prometheus.GaugeValue,
		descr:     "Total time on UPS battery",
		labels:    labels,
	},
	{
		id:        "timeLeft",
		name:      "apcups_time_left_seconds",
		valueType: prometheus.GaugeValue,
		descr:     "Time on UPS battery",
		labels:    labels,
	},
	{
		id:        "cumTimeOnBattery",
		name:      "apcups_cum_time_on_battery_seconds",
		valueType: prometheus.GaugeValue,
		descr:     "Cumululative Time on UPS battery",
		labels:    labels,
	},
	{
		id:        "batteryVoltage",
		name:      "apcups_battery_volts",
		valueType: prometheus.GaugeValue,
		descr:     "UPS Battery Voltage",
		labels:    labels,
	},
	{
		id:        "lineVoltage",
		name:      "apcups_line_volts",
		valueType: prometheus.GaugeValue,
		descr:     "UPS Line Voltage",
		labels:    labels,
	},
	{
		id:        "nomBatteryVoltage",
		name:      "apcups_nom_battery_volts",
		valueType: prometheus.GaugeValue,
		descr:     "UPS Nominal Battery Voltage",
		labels:    labels,
	},
	{
		id:        "nomInputVoltage",
		name:      "apcups_nom_input_volts",
		valueType: prometheus.GaugeValue,
		descr:     "UPS Nominal Input Voltage",
		labels:    labels,
	},
	{
		id:        "numTransfers",
		name:      "apcups_numtransfers",
		valueType: prometheus.GaugeValue,
		descr:     "Number of transfers to battery since apcupsd startup",
		labels:    append(labels, "lasttransfer", "timetransfertobattery", "timetransferfrombattery"),
	},
}

// Describe implements Prometheus.Collector.
func (c collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- prometheus.NewDesc("dummy", "dummy", nil, nil)
}

func (c collector) Collect(ch chan<- prometheus.Metric) {
	gatherStart := time.Now()

	data, _ := retrieveData(c.target)
	gatherDuration := time.Now().Sub(gatherStart)

	info, _ := transformData(data)
	log.Printf("%+v", info)

	for _, m := range metrics {
		switch m.id {
		case "status":
			var v float64
			var s string
			for _, stat := range statusList {
				if stat == info.status {
					v = 1
					s = stat
				} else {
					v = 0
					s = stat
				}
			}
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(m.name, m.descr, m.labels, nil),
				m.valueType,
				v,
				info.hostname, info.upsName, s, info.upsModel, info.batteryDate)
		case "statusNumeric":
			var v float64
			var s string
			for i, stat := range statusList {
				if stat == info.status {
					v = float64(i)
					s = stat
				}
			}
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(m.name, m.descr, m.labels, nil),
				m.valueType,
				v,
				info.hostname, info.upsName, s, info.upsModel, info.batteryDate)
		case "collectSeconds":
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(m.name, m.descr, m.labels, nil),
				m.valueType,
				gatherDuration.Seconds(),
				info.hostname, info.upsName)
		case "nominalPower":
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(m.name, m.descr, m.labels, nil),
				m.valueType,
				info.nomPower,
				info.hostname, info.upsName)
		case "batteryChargePercent":
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(m.name, m.descr, m.labels, nil),
				m.valueType,
				info.batteryChargePercent,
				info.hostname, info.upsName)
		case "timeOnBattery":
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(m.name, m.descr, m.labels, nil),
				m.valueType,
				info.timeOnBattery.Seconds(),
				info.hostname, info.upsName)
		case "timeLeft":
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(m.name, m.descr, m.labels, nil),
				m.valueType,
				info.timeLeft.Seconds(),
				info.hostname, info.upsName)
		case "cumTimeOnBattery":
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(m.name, m.descr, m.labels, nil),
				m.valueType,
				info.cumTimeOnBattery.Seconds(),
				info.hostname, info.upsName)
		case "loadPercent":
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(m.name, m.descr, m.labels, nil),
				m.valueType,
				info.loadPercent,
				info.hostname, info.upsName)
		case "batteryVoltage":
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(m.name, m.descr, m.labels, nil),
				m.valueType,
				info.batteryVoltage,
				info.hostname, info.upsName)
		case "lineVoltage":
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(m.name, m.descr, m.labels, nil),
				m.valueType,
				info.lineVoltage,
				info.hostname, info.upsName)
		case "nomBatteryVoltage":
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(m.name, m.descr, m.labels, nil),
				m.valueType,
				info.nomBatteryVoltage,
				info.hostname, info.upsName)
		case "nomInputVoltage":
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(m.name, m.descr, m.labels, nil),
				m.valueType,
				info.nomInputVoltage,
				info.hostname, info.upsName)
		case "numTransfers":
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(m.name, m.descr, m.labels, nil),
				m.valueType,
				info.numTransfers,
				info.hostname, info.upsName, info.lastTransfer, info.timeTransferToBattery.Format("2006-01-02 15:04:05 -0700"), info.timeTransferFromBattery.Format("2006-01-02 15:04:05 -0700"))
		}
	}
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
	if t == "" {
		return 0, nil
	}
	chunks := strings.Split(t, " ")
	fmtStr := chunks[0] + string(strings.ToLower(chunks[1])[0])
	return time.ParseDuration(fmtStr)
}

// parse generic units, splitting of units name and converting to float
func parseUnits(v string) (float64, error) {
	if v == "" {
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
