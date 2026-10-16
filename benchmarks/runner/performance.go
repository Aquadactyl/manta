package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

func (h *harness) performance() {
	h.startAll()
	payload := make([]byte, 16<<20)
	_, err := rand.Read(payload)
	must(err)
	expectedHash := sha256.Sum256(payload)
	for _, k := range []string{"manta", "wings"} {
		n := h.Nodes[k]
		s := h.find(n, "paper")
		sc, c := h.sftp(n, s)
		must(sc.MkdirAll("bench/fixtures"))
		for i := 0; i < 1000; i++ {
			f, err := sc.Create(fmt.Sprintf("bench/fixtures/f%04d.txt", i))
			must(err)
			_, err = f.Write(bytes.Repeat([]byte("x"), 256))
			must(err)
			must(f.Close())
		}
		must(sc.Close())
		must(c.Close())
	}
	for round := 0; round < 3; round++ {
		order := []string{"manta", "wings"}
		if round%2 == 1 {
			order = []string{"wings", "manta"}
		}
		for _, k := range order {
			n := h.Nodes[k]
			s := h.find(n, "paper")
			base := "/api/servers/" + s.UUID
			for _, route := range []struct{ name, path string }{{"api_status", base}, {"directory_1000", base + "/files/list-directory?directory=/bench/fixtures"}} {
				for _, workers := range []int{1, 16, 32} {
					h.api(n, "GET", route.path, nil)
					var next atomic.Int64
					var failed atomic.Int64
					lat := make([]float64, 400)
					start := time.Now()
					var wg sync.WaitGroup
					for worker := 0; worker < workers; worker++ {
						wg.Add(1)
						go func() {
							defer wg.Done()
							for {
								idx := int(next.Add(1) - 1)
								if idx >= len(lat) {
									return
								}
								t := time.Now()
								status, _, err := h.request(n, "GET", route.path, nil, false, true)
								lat[idx] = float64(time.Since(t).Microseconds()) / 1000
								if err != nil || status != 200 {
									failed.Add(1)
								}
							}
						}()
					}
					wg.Wait()
					row := distribution(lat)
					row["type"] = "measurement"
					row["test"] = route.name
					row["variant"] = k
					row["round"] = round
					row["concurrency"] = workers
					row["errors"] = failed.Load()
					row["requests_per_second"] = float64(len(lat)) / time.Since(start).Seconds()
					h.add(row)
				}
			}
			for _, operation := range []string{"http_write_16m", "http_read_16m", "sftp_write_16m", "sftp_read_16m"} {
				lat := []float64{}
				sc, c := h.sftp(n, s)
				for iteration := 0; iteration < 5; iteration++ {
					start := time.Now()
					var retrieved []byte
					switch operation {
					case "http_write_16m":
						status, _, err := h.request(n, "POST", base+"/files/write?file=/bench/payload.bin", payload, false, true)
						must(err)
						if status != 204 {
							panic(fmt.Sprintf("raw write status %d", status))
						}
					case "http_read_16m":
						status, b, err := h.request(n, "GET", base+"/files/contents?file=/bench/payload.bin", nil, false, true)
						must(err)
						if status != 200 {
							panic("HTTP content mismatch")
						}
						retrieved = b
					case "sftp_write_16m":
						f, err := sc.Create("bench/sftp.bin")
						must(err)
						_, err = io.Copy(f, bytes.NewReader(payload))
						must(err)
						must(f.Close())
					case "sftp_read_16m":
						f, err := sc.Open("bench/sftp.bin")
						must(err)
						b, err := io.ReadAll(f)
						must(err)
						must(f.Close())
						retrieved = b
					}
					lat = append(lat, float64(time.Since(start).Microseconds())/1000)
					if retrieved != nil && sha256.Sum256(retrieved) != expectedHash {
						panic("transferred content mismatch")
					}
				}
				must(sc.Close())
				must(c.Close())
				row := distribution(lat)
				row["type"] = "measurement"
				row["test"] = operation
				row["variant"] = k
				row["round"] = round
				row["bytes"] = len(payload)
				row["verified"] = true
				h.add(row)
			}
			for _, operation := range []string{"chmod_1000", "rename_1000"} {
				files := []any{}
				for i := 0; i < 1000; i++ {
					p := fmt.Sprintf("fixtures/f%04d.txt", i)
					if operation == "chmod_1000" {
						files = append(files, map[string]string{"file": p, "mode": "0644"})
					} else {
						files = append(files, map[string]string{"from": p, "to": p + ".moved"})
					}
				}
				method, path := "POST", "chmod"
				if operation == "rename_1000" {
					method, path = "PUT", "rename"
				}
				start := time.Now()
				h.api(n, method, base+"/files/"+path, map[string]any{"root": "/bench", "files": files})
				elapsed := time.Since(start)
				h.add(map[string]any{"type": "measurement", "test": operation, "variant": k, "round": round, "duration_ms": float64(elapsed.Microseconds()) / 1000})
				if operation == "rename_1000" {
					back := []any{}
					for i := 0; i < 1000; i++ {
						p := fmt.Sprintf("fixtures/f%04d.txt", i)
						back = append(back, map[string]string{"from": p + ".moved", "to": p})
					}
					h.api(n, "PUT", base+"/files/rename", map[string]any{"root": "/bench", "files": back})
				}
			}
			start := time.Now()
			archive := h.api(n, "POST", base+"/files/compress", map[string]any{"root": "/bench", "files": []string{"payload.bin", "fixtures"}})
			compressTime := time.Since(start)
			var stat struct {
				Name string `json:"name"`
			}
			must(json.Unmarshal(archive, &stat))
			if stat.Name == "" {
				panic("archive name missing")
			}
			start = time.Now()
			h.api(n, "POST", base+"/files/decompress", map[string]string{"root": "/bench", "file": stat.Name})
			extractTime := time.Since(start)
			b := h.api(n, "GET", base+"/files/contents?file="+url.QueryEscape("/bench/payload.bin"), nil)
			if sha256.Sum256(b) != expectedHash {
				panic("archive restore content mismatch")
			}
			h.add(map[string]any{"type": "measurement", "test": "archive_16m_1000_files", "variant": k, "round": round, "compress_ms": float64(compressTime.Microseconds()) / 1000, "extract_ms": float64(extractTime.Microseconds()) / 1000, "verified": true})
			h.api(n, "POST", base+"/files/delete", map[string]any{"root": "/bench", "files": []string{stat.Name}})
			h.websocketLoad(k, n, s, round)
		}
	}
}
