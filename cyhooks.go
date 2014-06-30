package main

import (
	"encoding/json"
	"fmt"
	"github.com/julienschmidt/httprouter"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type PushEvent struct {
	After      string `json:"after"`
	Ref        string `json:"ref"`
	Repository struct {
		Url string `json:"url"`
	} `json:"repository"`
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

// {"ref":"refs/heads/master",
// "after":"1d04f4b319c384316d7b366216d3bdf49cb5f471",
// "before":"3ba2264f7df80192ed8a573e6fa9dc0fb9375171",
// "created":false,"deleted":false,"forced":false,
// "compare":"https://github.com/ernado/poputchiki/compare/3ba2264f7df8...1d04f4b319c3",
// "commits":[{"id":"1d04f4b319c384316d7b366216d3bdf49cb5f471","distinct":true,
// "message":"added comments","timestamp":"2014-06-30T13:28:42+04:00"
// ,"url":"https://github.com/ernado/poputchiki/commit/1d04f4b319c384316d7b366216d3bdf49cb5f471",
// "author":{"name":"ernado","email":"ernado@ya.ru","username":"ernado"},
// "committer":{"name":"ernado","email":"ernado@ya.ru","username":"ernado"}
// ,"added":[],"removed":[],"modified":["transactions.go"]}],
// "head_commit":{"id":"1d04f4b319c384316d7b366216d3bdf49cb5f471"
// ,"distinct":true,"message":"added comments","timestamp":"2014-06-30T13:28:42+04:00",
// "url":"https://github.com/ernado/poputchiki/commit/1d04f4b319c384316d7b366216d3bdf49cb5f471",
// "author":{"name":"ernado","email":"ernado@ya.ru","username":"ernado"},
// "committer":{"name":"ernado","email":"ernado@ya.ru","username":"ernado"},
// "added":[],"removed":[],"modified":["transactions.go"]},

// "repository":{"id":20809005,"name":"poputchiki","url":"https://github.com/ernado/poputchiki",
// "description":"poputchiki-api","homepage":"cydev.poputchiki.ru",
// "watchers":0,"stargazers":0,"forks":0,"fork":false,"size":5248,
// "owner":{"name":"ernado","email":"ernado@ya.ru"},
// "private":true,"open_issues":0,
// "has_issues":true,"has_downloads":true,
// "has_wiki":true,"language":"Go","created_at":1402673981,
// "pushed_at":1404120511,"master_branch":"master"},
// "pusher":{"name":"ernado","email":"ernado@ya.ru"}}

func Index(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	decoder := json.NewDecoder(r.Body)
	event := new(PushEvent)
	decoder.Decode(event)
	user, repo := event.Get()
	sshPath := event.String()
	go func() {
		var err error
		var out []byte

		log.Println("updating", repo)
		if !event.Master() {
			log.Println("not mester, aborting")
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
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err = os.MkdirAll(cmd.Dir, 0777); err != nil {
			log.Println(cmd.Dir, err)
			return
		}
		out, err = cmd.Output()
		if len(out) > 0 {
			log.Print(string(out))
		} else {
			log.Println("no output")
		}
		if err != nil {
			log.Println("failed to pull:", err)
			if err := os.RemoveAll(path); err != nil {
				log.Println(err)
			}
			return
		}
		log.Println("updating")
		cmd = exec.Command("fab", "update")
		cmd.Dir = path
		out, err = cmd.Output()
		log.Print(string(out))
		if err != nil {
			log.Print("failed to update:", err)
			return
		}
		log.Println(repo, "updated")
	}()
	log.Println("webhook for", repo, "processed")
	fmt.Fprintln(w, "ok")
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	router := httprouter.New()
	router.POST("/webhook", Index)
	log.Fatal(http.ListenAndServe(":8081", router))
}
