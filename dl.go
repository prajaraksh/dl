package dl

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	aria "github.com/zyxar/argo/rpc"
)

var baseDir, _ = os.Getwd()
var homeDir, _ = os.UserHomeDir()
var logDir = filepath.Join(homeDir, ".dl")

// Opts for downloader.
type Opts struct {
	// BaseDir for Downloader
	BaseDir string

	LogPrefix string

	// ConcDls ,max no.of downloads at any point of time
	ConcDls int

	// OnComplete channel is used to inform user once a download is done
	OnComplete chan struct{}

	// AriaArgs are passed on to aria
	AriaArgs []string

	// fic file input channel, downloads passed to workers
	fic chan *File

	wg sync.WaitGroup

	cmd    *exec.Cmd
	client aria.Client
	gidMap *sync.Map
	notify ariaNotifier
}

// DL Downloader
type DL Opts

// New downloader with given opts,
// unset fields are set internally.
func New(opts *Opts) *DL {
	if opts != nil {
		return setDefaultOpts(opts)
	}

	return setDefaultOpts(&Opts{})
}

func getLogFile(logPrefix string) io.Writer {

	err := os.MkdirAll(logDir, os.ModePerm)
	if err != nil {
		log.Fatalln(err)
	}

	now := time.Now().Format("Jan-2-2006-15:04:05-000")
	if logPrefix != "" {
		now = logPrefix + "-" + now
	}

	logFile := filepath.Join(logDir, now)

	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_RDWR, os.ModePerm)
	if err != nil {
		log.Fatalln("Unable to create log file", err)
	}

	return f
}

var ariaSecret = "prajaraksh/dl"

func setDefaultOpts(opts *Opts) *DL {

	logFile := getLogFile(opts.LogPrefix)

	logrus.SetOutput(logFile)

	if opts.BaseDir == "" {
		opts.BaseDir = baseDir
	}

	if opts.ConcDls == 0 {
		opts.ConcDls = 3
	}

	opts.gidMap = &sync.Map{}
	opts.notify.gidMap = opts.gidMap

	var err error
	var port int

	opts.cmd, port, err = startAria(opts.AriaArgs, opts.ConcDls)
	if err != nil {
		logrus.Fatalln("unable to start aria", err)
	}

	// let aria2c start, so we can connect
	time.Sleep(time.Millisecond * 100)

	rpcURI := "ws://localhost:" + strconv.Itoa(port) + "/jsonrpc"

	opts.client, err = aria.New(
		context.Background(),
		rpcURI,
		ariaSecret,
		time.Second,
		opts.notify,
	)

	if err != nil {
		logrus.Fatalln("unable to create client:", err)
	}

	if _, err := opts.client.GetVersion(); err != nil {
		logrus.Fatalln("unable to get version info:", err)
	}

	// unexported fields
	opts.fic = make(chan *File)

	d := (*DL)(opts)

	// init workers
	d.workers()

	go updateFileStatuses(d.client, d.gidMap)

	return d
}

// Download given set of files.
// Download is a blocking call, if more than `ConcDls` are in progress.
func (dl *DL) Download(files Files) {
	for _, f := range files {
		dl.fic <- f
	}
}

// Close this downloader
// waits till all downloads are completed
func (dl *DL) Close() {
	close(dl.fic)
	dl.wg.Wait()

	p.Wait()

	refreshTicker.Stop()

	if ok, err := dl.client.ForceShutdown(); err != nil {
		logrus.Println(ok, err)
	}
}

// URL ,Download files with single url
func URL(url ...string) Files {
	files := make(Files, 0, len(url))
	for _, u := range url {
		files = append(files, &File{URLs: []string{u}})
	}
	return files
}

// URLs ,Download files with multiple urls
func URLs(urls ...[]string) Files {
	files := make(Files, 0, len(urls))
	for _, us := range urls {
		files = append(files, &File{URLs: us})
	}
	return files
}

// Dir sets `dir` for all given files
func (fs Files) Dir(dir string) Files {
	for _, f := range fs {
		f.Dir = dir
	}
	return fs
}

// OnComplete sets `onComplete` for all given files
func (fs Files) OnComplete(onComplete chan struct{}) Files {
	for _, f := range fs {
		f.OnComplete = onComplete
	}
	return fs
}

// Cookies sets `Cookies` for all given files
func (fs Files) Cookies(cookies []*http.Cookie) Files {
	for _, f := range fs {
		f.Cookies = cookies
	}
	return fs
}

// ConcReqs sets `concReqs` for all given files
func (fs Files) ConcReqs(concReqs int) Files {
	for _, f := range fs {
		f.ConcReqs = concReqs
	}
	return fs
}

// Referer sets `referer` for all given files
func (fs Files) Referer(referer string) Files {
	for _, f := range fs {
		f.Referer = referer
	}
	return fs
}

// CB sets `fns` for all given files
// also see `GroupCB()`.
func (fs Files) CB(name, operation string, fns ...func(f *File)) Files {
	for _, f := range fs {
		f.CBs = fns
		f.CBName = name
		f.CBOperation = operation
	}
	return fs
}

// GroupCB calls provided `fns` once all files `fs` are downloaded.
// Can be used for merging multiple files,
// or for any operation on multiple set of files
// `operation` is replaced with ``, onComplete.
// if `replaceBars` true, removes `fs` bars and replaces with spinnerBar.
func (dl *DL) GroupCB(fs Files, name, operation string, replaceBars bool, fns ...func(fs Files)) Files {
	rcv := make(chan struct{})

	for _, f := range fs {
		f.cbChan = rcv
		f.removeBar = replaceBars
	}

	// once a file is downloaded,
	// we get informed through `rcv`,
	// once all given set of files are downloaded,
	// we call provided callback functions
	n := len(fs)

	dl.wg.Add(1)

	go func() {
		defer dl.wg.Done()
		for range rcv {
			n--
			if n == 0 {
				close(rcv)
				break
			}
		}

		bb := groupCBFuncsBar(name, operation, len(fns))
		for _, fn := range fns {
			if fn != nil {
				fn(fs)
				if bb != nil {
					bb.Increment()
				}
			}
		}
	}()

	return fs
}

func (dl *DL) download(f *File) {
	// set default options
	if f.Name == "" {
		f.Name = extractName(f.URLs[0])
	}

	if f.ConcReqs == 0 {
		f.ConcReqs = 3
	}

	if f.OnComplete == nil {
		f.OnComplete = dl.OnComplete
	}

	if dl.BaseDir != "" {
		f.Dir = filepath.Join(dl.BaseDir, f.Dir)
	}

	f.download(dl.client, dl.gidMap)
}

// workers are dispatched
func (dl *DL) workers() {

	dl.wg.Add(dl.ConcDls)

	for i := 0; i < dl.ConcDls; i++ {

		go func(i int) {
			logrus.Infoln("Worker", i, "started")
			defer dl.wg.Done()
			for f := range dl.fic {
				f.workerID = i
				dl.download(f)
			}
		}(i)
	}

}
