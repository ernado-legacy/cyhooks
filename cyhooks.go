package main

import (
	"encoding/gob"
	"encoding/json"
	"fmt"
	"github.com/julienschmidt/httprouter"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var counter int64
var events = make(map[int64]*HookEvent)
var workdir = "cache"
var dumpfile = filepath.Join(workdir, "dump.gob")

func Load() {
	log.Println("loadingg dump file")
	f, err := os.OpenFile(dumpfile, os.O_RDONLY, 0666)
	if err != nil {
		log.Println("no dump file")
		return
	}
	defer f.Close()
	decoder := gob.NewDecoder(f)
	if err := decoder.Decode(&events); err != nil {
		log.Println("failed to decode events", err)
		return
	}
	counter = int64(len(events))
}

func Dump() {
	log.Println("saving dump file")
	f, err := os.OpenFile(dumpfile, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		log.Println("[dump]", err)
		return
	}
	defer f.Close()
	encoder := gob.NewEncoder(f)
	if err := encoder.Encode(events); err != nil {
		log.Println(err)
	}
	log.Println("saved")
}

type PushEvent struct {
	After      string `json:"after"`
	Ref        string `json:"ref"`
	Repository struct {
		Url string `json:"url"`
	} `json:"repository"`
}

type HookEvent struct {
	Id        int64
	Repo      string
	Status    string
	Time      time.Time
	OutputRaw string
	Ok        bool
}

func (e *HookEvent) Write(p []byte) (n int, err error) {
	e.OutputRaw += string(p)
	return len(p), nil
}

func (e *HookEvent) Date() string {
	return e.Time.Truncate(time.Second).Format("2006-01-02 15:04:05")
}

func (e *HookEvent) Output() template.HTML {
	return template.HTML(strings.Replace(e.OutputRaw, "\n", "<br>", -1))
}

func (e *HookEvent) Fail() {
	e.Ok = false
	e.SetStatus("failed")
}

func (e *HookEvent) Build() {
	e.Ok = true
	e.SetStatus("ok")
}

func NewHookEvent(repo string) *HookEvent {
	h := new(HookEvent)
	events[counter] = h
	h.Id = counter
	counter += 1
	h.Status = "starting"
	h.Repo = repo
	h.Time = time.Now()
	return h
}

func (e *HookEvent) SetStatus(status string) {
	e.Status = status
}

func Index(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	t, err := template.ParseFiles("static/index.html")
	if err != nil {
		log.Println(err)
		return
	}

	// getting last 10 events
	j := int64(0)
	count := int64(10)
	if counter <= count {
		count = counter
	}
	lastEvents := make([]*HookEvent, count)
	for i := counter - 1; j < count; i -= 1 {
		lastEvents[j] = events[i]
		j += 1
	}

	if err = t.Execute(w, lastEvents); err != nil {
		log.Println(err)
		return
	}
}

func (event *PushEvent) Master() bool {
	return event.Ref == "refs/heads/master"
}

func (event *PushEvent) Get() (string, string) {
	parts := strings.Split(event.Repository.Url, "/")
	return parts[len(parts)-2], parts[len(parts)-1]
}

func (event *PushEvent) String() string {
	user, repo := event.Get()
	return fmt.Sprintf("git@github.com:%s/%s.git", user, repo)
}

func Handle(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	decoder := json.NewDecoder(r.Body)
	event := new(PushEvent)
	decoder.Decode(event)
	user, repo := event.Get()
	sshPath := event.String()
	go func() {
		defer Dump()
		var err error
		pushEvent := NewHookEvent(repo)
		buffer := pushEvent
		stdout := io.MultiWriter(os.Stdout, buffer)
		stderr := io.MultiWriter(os.Stderr, buffer)
		log.SetOutput(stdout)
		defer log.SetOutput(os.Stdout)

		log.Println("updating", repo)
		if !event.Master() {
			log.Println("not master, aborting")
			pushEvent.Build()
			return
		}

		path := filepath.Join("cache", user, repo)
		cmd := new(exec.Cmd)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			log.Println("cloning", sshPath, "to", path)
			cmd = exec.Command("git", "clone", sshPath)
			cmd.Dir = filepath.Join("cache", user)
		} else {
			log.Println("pulling", sshPath)
			cmd = exec.Command("git", "pull")
			cmd.Dir = path
		}

		cmd.Stderr = stderr
		cmd.Stdout = stdout
		if err = os.MkdirAll(cmd.Dir, 0777); err != nil {
			log.Println(cmd.Dir, err)
			pushEvent.Fail()
			return
		}
		pushEvent.SetStatus("pulling")
		if err = cmd.Run(); err != nil {
			log.Println("failed to pull:", err)
			pushEvent.Fail()
			return
		}

		log.Println("updating")
		pushEvent.SetStatus("updating")
		cmd = exec.Command("fab", "update")
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Dir = path
		err = cmd.Run()
		if err != nil {
			log.Print("failed to update:", err)
			pushEvent.Fail()
			return
		}
		pushEvent.Build()
		log.Println(repo, "updated")
	}()
	log.Println("webhook for", repo, "processed")
	fmt.Fprintln(w, "ok")
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	router := httprouter.New()
	router.POST("/webhook", Handle)
	router.GET("/webhook", Index)
	router.ServeFiles("/webhook/*filepath", http.Dir("static"))
	Load()
	log.Println("listening on :8081")
	log.Fatal(http.ListenAndServe(":8081", router))
}
