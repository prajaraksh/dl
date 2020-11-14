package dl

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/sirupsen/logrus"
)

var testDir = "test"
var NumOfFiles = 10

func TestDownload(t *testing.T) {
	if !fileExists(testDir) {
		os.MkdirAll(testDir, os.ModePerm)
		for i := 0; i < NumOfFiles; i++ {
			generateData(testDir, strconv.Itoa(i), 1e8)
		}
	}

	// start a webserver
	port := closedPortNum()
	server := server("localhost:"+strconv.Itoa(port), testDir)

	files := make([]string, 0, NumOfFiles)
	for i := 0; i < NumOfFiles; i++ {
		files = append(files, fmt.Sprintf("http://localhost:%d/%d", port, i))
	}

	fs := URL(files...)

	d := New(&Opts{BaseDir: "test/output", AriaArgs: []string{"--max-overall-download-limit=4M"}})
	d.Download(fs)
	d.Close()

	err := server.Shutdown(context.Background())
	logrus.Println("Close", err)

}

func generateData(dir string, name string, size int64) {
	f, err := os.Create(filepath.Join(dir, name))
	defer f.Close()
	if err != nil {
		log.Println(err)
	}

	data := make([]byte, size)
	rand.Read(data)
	f.Write(data)
}

func server(addr, dir string) *http.Server {
	server := &http.Server{Addr: addr, Handler: http.FileServer(http.Dir(dir))}

	listenChan := make(chan struct{})

	go func() {
		listenChan <- struct{}{}
		server.ListenAndServe()
	}()

	<-listenChan
	return server
}
