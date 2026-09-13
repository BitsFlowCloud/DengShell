package app

import (
	"fmt"
	"math"
	"strings"
)

type diagnosticHop struct {
	Address                          string
	ASN                              string
	Sent, Received                   int
	Last, Total, Square, Best, Worst float64
}

func (h *diagnosticHop) add(probe ICMPProbeResult) {
	if probe.Status == "unavailable" {
		return
	}
	h.Sent++
	if !probe.Responded {
		return
	}
	h.Received++
	h.Address = probe.Address
	h.Last = probe.Milliseconds
	h.Total += probe.Milliseconds
	h.Square += probe.Milliseconds * probe.Milliseconds
	if h.Received == 1 || probe.Milliseconds < h.Best {
		h.Best = probe.Milliseconds
	}
	if probe.Milliseconds > h.Worst {
		h.Worst = probe.Milliseconds
	}
}
func windowsDiagnosticOutput(header string, hops []diagnosticHop, round int) string {
	var out strings.Builder
	out.WriteString(header)
	fmt.Fprintf(&out, "探测轮次 %d / 10 · Windows 原生 ICMP TTL，最多 30 跳\n\n", round)
	fmt.Fprintln(&out, "Hop  ASN                   Address                                   Loss%  Snt   Last    Avg   Best  Worst  StDev")
	for i, h := range hops {
		address := h.Address
		asn := h.ASN
		if asn == "" {
			asn = "N/A"
		}
		if address == "" {
			address = "???"
		}
		loss, avg, stdev := "—", 0.0, 0.0
		if h.Sent > 0 {
			loss = fmt.Sprintf("%.1f", 100*float64(h.Sent-h.Received)/float64(h.Sent))
		}
		if h.Received > 0 {
			avg = h.Total / float64(h.Received)
			stdev = math.Sqrt(math.Max(0, h.Square/float64(h.Received)-avg*avg))
		}
		if h.Received == 0 {
			fmt.Fprintf(&out, "%3d  %-21s %-40s %5s %4d      —      —      —      —      —\n", i+1, asn, address, loss, h.Sent)
		} else {
			fmt.Fprintf(&out, "%3d  %-21s %-40s %5s %4d %6.1f %6.1f %6.1f %6.1f %6.1f\n", i+1, asn, address, loss, h.Sent, h.Last, avg, h.Best, h.Worst, stdev)
		}
	}
	return out.String()
}
