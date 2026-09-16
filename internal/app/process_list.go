package app

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const processPageLimit = 200

type processListQuery struct {
	Page, Size   int
	Sort, Search string
	Desc         bool
}
type ProcessPage struct {
	Processes                []Process         `json:"processes"`
	Page                     int               `json:"page"`
	PageSize                 int               `json:"pageSize"`
	Total                    int               `json:"total"`
	Matched                  int               `json:"matched"`
	Pages                    int               `json:"pages"`
	Sample                   ProcessSampleInfo `json:"sample"`
	NextSampleInMilliseconds int64             `json:"nextSampleInMilliseconds"`
}
type processListCache struct {
	mu     sync.Mutex
	source *rawStats
	query  processListQuery
	order  []int
}

func parseProcessQuery(r *http.Request) (processListQuery, error) {
	q := processListQuery{Page: 1, Size: 100, Sort: "cpu", Desc: true}
	v := r.URL.Query()
	for key, target := range map[string]*int{"page": &q.Page, "pageSize": &q.Size} {
		if v.Get(key) != "" {
			n, err := strconv.Atoi(v.Get(key))
			if err != nil || n < 1 {
				return q, errors.New("进程页码和每页数量应为正整数")
			}
			*target = n
		}
	}
	if q.Size > processPageLimit || q.Page > 1000000 {
		return q, errors.New("每页最多显示 200 个进程")
	}
	if v.Get("sort") != "" {
		q.Sort = v.Get("sort")
	}
	switch q.Sort {
	case "cpu", "memory", "pid", "name", "state":
	default:
		return q, errors.New("不支持的进程排序方式")
	}
	if v.Get("direction") != "" {
		if v.Get("direction") != "asc" && v.Get("direction") != "desc" {
			return q, errors.New("进程排序方向无效")
		}
		q.Desc = v.Get("direction") == "desc"
	}
	q.Search = strings.ToLower(strings.TrimSpace(v.Get("search")))
	if len(q.Search) > 256 {
		return q, errors.New("进程搜索内容过长")
	}
	return q, nil
}

func (s *Session) processPage(ctx context.Context, q processListQuery) (ProcessPage, error) {
	// The sidebar and full list share one scan/baseline; sorting or flipping a
	// page cannot create a second remote scan or bypass its adaptive interval.
	s.statsMu.Lock()
	if err := ctx.Err(); err != nil {
		s.statsMu.Unlock()
		return ProcessPage{}, err
	}
	if s.processPrevious == nil || !time.Now().Before(s.processNextSampleAt) {
		if err := s.collectProcessesOnly(ctx); err != nil {
			s.statsMu.Unlock()
			return ProcessPage{}, err
		}
	}
	snapshot, due := s.processPrevious, s.processNextSampleAt
	s.statsMu.Unlock()
	page, err := s.processList.page(ctx, snapshot, q)
	if err != nil {
		return page, err
	}
	page.NextSampleInMilliseconds = nextSampleMilliseconds(due)
	s.readProcessUsers(ctx, page.Processes)
	return page, ctx.Err()
}

func (c *processListCache) page(ctx context.Context, snapshot *rawStats, q processListQuery) (ProcessPage, error) {
	result := ProcessPage{Processes: []Process{}, Page: q.Page, PageSize: q.Size}
	if snapshot == nil {
		return result, nil
	}
	result.Total, result.Sample = len(snapshot.Processes), snapshot.ProcessSample
	c.mu.Lock()
	defer c.mu.Unlock()
	key := q
	key.Page, key.Size = 0, 0
	if c.source != snapshot || c.query != key {
		order := make([]int, 0, len(snapshot.Processes))
		for i, p := range snapshot.Processes {
			if i%1024 == 0 {
				if err := ctx.Err(); err != nil {
					return result, err
				}
			}
			if q.Search == "" || strings.Contains(strings.ToLower(p.Name), q.Search) || strings.Contains(strconv.Itoa(p.PID), q.Search) || strings.EqualFold(p.State, q.Search) {
				order = append(order, i)
			}
		}
		sort.Slice(order, func(i, j int) bool { return processLess(snapshot.Processes[order[i]], snapshot.Processes[order[j]], q) })
		c.source, c.query, c.order = snapshot, key, order
	}
	result.Matched = len(c.order)
	result.Pages = max(1, (result.Matched+q.Size-1)/q.Size)
	result.Page = min(q.Page, result.Pages)
	start := (result.Page - 1) * q.Size
	for _, idx := range c.order[start:min(start+q.Size, len(c.order))] {
		result.Processes = append(result.Processes, snapshot.Processes[idx])
	}
	return result, nil
}

func processLess(a, b Process, q processListQuery) bool {
	cmp := 0
	switch q.Sort {
	case "cpu":
		if a.CPUReady != b.CPUReady {
			return a.CPUReady
		}
		if a.CPUReady {
			if a.CPU < b.CPU {
				cmp = -1
			} else if a.CPU > b.CPU {
				cmp = 1
			}
		}
	case "memory":
		if a.MemoryReady != b.MemoryReady {
			return a.MemoryReady
		}
		if a.MemoryReady {
			if a.Memory < b.Memory {
				cmp = -1
			} else if a.Memory > b.Memory {
				cmp = 1
			}
		}
	case "pid":
		if a.PID < b.PID {
			cmp = -1
		} else if a.PID > b.PID {
			cmp = 1
		}
	case "name":
		cmp = strings.Compare(a.Name, b.Name)
	case "state":
		cmp = strings.Compare(a.State, b.State)
	}
	if cmp == 0 {
		return a.PID < b.PID
	}
	if q.Desc {
		return cmp > 0
	}
	return cmp < 0
}

func (a *App) processesHTTP(w http.ResponseWriter, r *http.Request) {
	s, err := a.session(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, err)
		return
	}
	q, err := parseProcessQuery(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	page, err := s.processPage(r.Context(), q)
	respond(w, page, err)
}
