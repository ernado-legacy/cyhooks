package main

import (
	"bytes"
	"crypto/rand"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
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

const (
	envSlackToken   = "SLACK_TOKEN"
	envSlackCompany = "SLACK_COMPANY"
)

var (
	counter       int64
	events        = make(map[int64]*HookEvent)
	workdir       = "cache"
	dumpfile      = filepath.Join(workdir, "dump.gob")
	globalUpdates = make(chan RealtimeEvent)
	listeners     = make(map[string]chan RealtimeEvent)
	panelTemplate *template.Template
	indexTemplate *template.Template
	slackToken    = os.Getenv(envSlackToken)
	slackCompany  = os.Getenv(envSlackCompany)
	slackUrl      = fmt.Sprintf("https://%s.slack.com/services/hooks/incoming-webhook?token=%s", slackCompany, slackToken)
)

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

type SlackAttachment struct {
	Text    string            `json:"text"`
	Pretext string            `json:"pretext"`
	Color   string            `json:"color"`
	Fields  []SlackEventField `json:"fields"`
}

type SlackEventField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

type SlackEvent struct {
	Text        string            `json:"text"`
	Color       string            `json:"color"`
	Attachments []SlackAttachment `json:"attachments"`
}

func SlackPush(e *HookEvent, repo, status, color string) error {
	if len(slackCompany) == 0 || len(slackToken) == 0 {
		return nil
	}
	buffer := new(bytes.Buffer)
	encoder := json.NewEncoder(buffer)
	event := new(SlackEvent)
	event.Text = fmt.Sprintf("Build of %s is %s", repo, status)
	if e != nil {
		attachment := SlackAttachment{}
		attachment.Color = color
		field := SlackEventField{Title: "Repository", Value: repo}
		field2 := SlackEventField{Title: "Duration", Value: e.Duration()}
		field3 := SlackEventField{Title: "Date", Value: e.Date()}
		attachment.Fields = []SlackEventField{field, field2, field3}
		event.Attachments = []SlackAttachment{attachment}
	}

	if err := encoder.Encode(event); err != nil {
		return err
	}
	req, err := http.NewRequest("POST", slackUrl, buffer)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK || res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("Invalid status: %s (%d)", res.Status, res.StatusCode)
	}
	return nil
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
	Start     time.Time
	Stop      time.Time
	OutputRaw string
	Ok        bool
}

func (e *HookEvent) Write(p []byte) (n int, err error) {
	data := string(p)
	data = strings.Replace(data, "\n", "<br>", -1)
	e.OutputRaw += data
	go func() {
		globalUpdates <- RealtimeEvent{e.Id, "write", data}
	}()
	return len(p), nil
}

func (e *HookEvent) Date() string {
	return e.Start.Truncate(time.Second).Format("2006-01-02 15:04:05")
}

func (e *HookEvent) Duration() string {
	end := time.Time{}
	if end == e.Stop {
		end = time.Now()
	} else {
		end = e.Stop
	}
	end = end.Truncate(100 * time.Millisecond)
	return end.Sub(e.Start.Truncate(100 * time.Millisecond)).String()
}

func (e *HookEvent) Output() template.HTML {
	return template.HTML(strings.Replace(e.OutputRaw, "\n", "<br>", -1))
}

func (e *HookEvent) SetStop() {
	e.Stop = time.Now()
}

func (e *HookEvent) Fail() {
	e.Ok = false
	e.SetStatus("failed")
	update := map[string]interface{}{"ok": false}
	if err := SlackPush(e, e.Repo, "failed", "danger"); err != nil {
		log.Println("Slack:", err)
	}
	globalUpdates <- RealtimeEvent{e.Id, "update", update}
}

func (e *HookEvent) Build() {
	e.Ok = true
	e.SetStatus("ok")
	update := map[string]interface{}{"ok": true}
	if err := SlackPush(e, e.Repo, "ok", "good"); err != nil {
		log.Println("Slack:", err)
	}
	globalUpdates <- RealtimeEvent{e.Id, "update", update}
}

func (e *HookEvent) Render() template.HTML {
	t := panelTemplate
	var b bytes.Buffer
	err := t.Execute(&b, e)
	if err != nil {
		log.Println(err)
		return ""
	}
	return template.HTML(b.Bytes())
}

func NewHookEvent(repo string) *HookEvent {
	h := new(HookEvent)
	events[counter] = h
	h.Id = counter
	counter += 1
	h.Status = "starting"
	h.Repo = repo
	h.Start = time.Now()
	globalUpdates <- RealtimeEvent{h.Id, "new", string(h.Render())}
	return h
}

func (e *HookEvent) SetStatus(status string) {
	e.Status = status
	update := map[string]string{"set_status": status}
	globalUpdates <- RealtimeEvent{e.Id, "update", update}
}

func Index(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var err error
	indexTemplate, err = template.ParseFiles("static/index.html")
	if err != nil {
		log.Println(err)
		return
	}
	t := indexTemplate

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

func (event *PushEvent) Dev() bool {
	return event.Ref == "refs/heads/dev"
}

func (event *PushEvent) Get() (string, string) {
	parts := strings.Split(event.Repository.Url, "/")
	if len(parts) < 2 {
		log.Println("bad repo url", event.Repository.Url)
		return "", ""
	}
	return parts[len(parts)-2], parts[len(parts)-1]
}

func (event *PushEvent) String() string {
	user, repo := event.Get()
	return fmt.Sprintf("git@github.com:%s/%s.git", user, repo)
}

func Handle(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	defer func() {
		err := recover()
		if err != nil {
			log.Println("recovered:", err)
		}
	}()
	decoder := json.NewDecoder(r.Body)
	event := new(PushEvent)
	decoder.Decode(event)
	user, repo := event.Get()
	// sshPath := event.String()
	go func() {
		defer func() {
			err := recover()
			if err != nil {
				log.Println("recovered:", err)
			}
		}()
		defer Dump()

		ticker := time.NewTicker(time.Millisecond * 100)
		defer ticker.Stop()
		var err error
		pushEvent := NewHookEvent(repo)
		buffer := pushEvent
		stdout := io.MultiWriter(os.Stdout, buffer)
		stderr := io.MultiWriter(os.Stderr, buffer)
		log.SetOutput(stdout)
		defer log.SetOutput(os.Stdout)
		defer pushEvent.SetStop()

		log.Println("handle started")
		log.Println("updating", user, repo)
		if err := SlackPush(nil, repo, "started", ""); err != nil {
			log.Println("Slack:", err)
		}
		go func() {
			for _ = range ticker.C {
				update := map[string]string{"duration": pushEvent.Duration()}
				event := RealtimeEvent{pushEvent.Id, "update", update}
				globalUpdates <- event
			}
		}()
		log.Println("is dev", event.Dev())
		if !event.Dev() {
			log.Println("not dev, aborting")
			pushEvent.Build()
			return
		}

		cmd := exec.Command("/home/ernado/bin/fly", "-t", "ci",
			"check-resource", "-r" , "stun/dev")
		cmd.Stderr = stderr
		cmd.Stdout = stdout
		log.Println("updating")
		pushEvent.SetStatus("updating")
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

func checkOrigin(r *http.Request) bool {
	return true
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     checkOrigin,
}

type RealtimeEvent struct {
	Id   int64       `json:"id"`
	Type string      `json:"type"`
	Body interface{} `json:"body"`
}

func Translate() {
	for event := range globalUpdates {
		for key := range listeners {
			listeners[key] <- event
		}
	}
}

func Realtime(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	log.Println("realtime", r.RemoteAddr)
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	defer conn.Close()
	b := make([]byte, 12)
	rand.Read(b)
	id := hex.EncodeToString(b)

	eventJson, _ := json.Marshal(RealtimeEvent{0, "id", id})
	conn.WriteMessage(websocket.TextMessage, eventJson)

	updates := make(chan RealtimeEvent)
	listeners[id] = updates
	defer close(updates)
	defer delete(listeners, id)

	for event := range updates {
		eventJson, _ := json.Marshal(event)
		conn.WriteMessage(websocket.TextMessage, eventJson)
	}
}

func main() {
	var err error
	indexTemplate, err = template.ParseFiles("static/index.html")
	if err != nil {
		log.Println(err)
		return
	}

	panelTemplate, err = template.ParseFiles("static/panel.html")
	if err != nil {
		log.Println(err)
		return
	}

	runtime.GOMAXPROCS(runtime.NumCPU())
	router := httprouter.New()
	router.POST("/webhook", Handle)
	router.GET("/webhook", Index)
	router.GET("/webhook/realtime", Realtime)
	router.ServeFiles("/webhook/static/*filepath", http.Dir("static"))

	Load()
	go Translate()
	log.Println("listening on :8081")
	log.Fatal(http.ListenAndServe(":8081", router))
}
