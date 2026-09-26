//go:build windows

package app

import (
	"context"
	"fmt"
	"sync"
	"time"
)

func runLocalDiagnostic(ctx context.Context, target string, d *Diagnostic) error {
	header := diagnosticHeader("本机", target, "Windows 原生 ICMP 路径报告")
	header += "ASN：Team Cymru DNS（仅查询响应跳的公网 IP，使用系统 DNS）；N/A 表示内网地址或查询不可用。\n\n"
	asns := newDiagnosticASNTracker()
	hops := make([]diagnosticHop, 30)
	limit := 30
	reached := false
	for round := 1; round <= 10; round++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		results := make([]ICMPProbeResult, limit)
		semaphore := make(chan struct{}, 8)
		var group sync.WaitGroup
		for i := range results {
			group.Add(1)
			go func(i int) {
				defer group.Done()
				select {
				case semaphore <- struct{}{}:
					defer func() { <-semaphore }()
				case <-ctx.Done():
					results[i] = ICMPProbeResult{Status: "unavailable", Error: ctx.Err().Error()}
					return
				}
				results[i] = probeICMPTTL(ctx, target, byte(i+1), time.Second)
			}(i)
		}
		group.Wait()
		usable := false
		for i, probe := range results {
			hops[i].add(probe)
			if probe.Status != "unavailable" {
				usable = true
			}
			if probe.Status == "reply" && probe.Responded {
				limit = min(limit, i+1)
				reached = true
			}
		}
		asns.apply(ctx, hops[:limit])
		d.setOutput(windowsDiagnosticOutput(header, hops[:limit], round))
		if !usable {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("Windows ICMP 不可用：%s", results[0].Error)
		}
		if round < 10 {
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	asns.wait(ctx)
	asns.apply(ctx, hops[:limit])
	d.setOutput(windowsDiagnosticOutput(header, hops[:limit], 10))
	if !reached {
		fmt.Fprintln(d, "\n本次未收到终点响应；可能是终点禁用 ICMP、超出跳数上限或路径不可达。")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}
