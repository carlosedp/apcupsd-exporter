#!/bin/sh

/sbin/apcupsd

/apcupsd-exporter -listen-address=:9099
