/*
References:
 - https://docs.gitlab.com/ce/user/project/integrations/webhooks.html#merge-request-events
 - https://docs.gitlab.com/ce/api/commits.html#get-a-single-commit
 - https://docs.gitlab.com/ce/api/pipelines.html
 - https://docs.gitlab.com/ce/api/jobs.html
*/

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
)

type project struct {
	Name    string `json:"name"`
	WebURL  string `json:"web_url"`
	HTTPURL string `json:"http_url"`
}

type commit struct {
	ID           string    `json:"id"`
	Message      string    `json:"message"`
	LastPipeline *pipeline `json:"last_pipeline"`
	Timestamp    string    `json:"timestamp"`
}

type pipeline struct {
	ID int `json:"id"`
}

type job struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type objectAttributes struct {
	ID              int     `json:"id"`
	IID             int     `json:"iid"`
	TargetBranch    string  `json:"target_branch"`
	SourceBranch    string  `json:"source_branch"`
	SourceProjectID int64   `json:"source_project_id"`
	State           string  `json:"state"`
	MergeStatus     string  `json:"merge_status"`
	Source          project `json:"source"`
	LastCommit      commit  `json:"last_commit"`
	Action          string  `json:"action"`
	WorkInProgress  bool    `json:"work_in_progress"`
}

type webhookRequest struct {
	ObjectKind string           `json:"object_kind"`
	Attributes objectAttributes `json:"object_attributes"`
}

type tokenResponse struct {
	ID          int    `json:"id"`
	DeletedAt   string `json:"deleted_at"`
	Token       string `json:"token"`
	Description string `json:"description"`
}

var listenAddr = flag.String("listen", ":8080", "HTTP listen address")
var triggerToken = flag.String("token", "", "HTTP trigger token")
var privateToken = flag.String("private-token", "", "User PRIVATE-TOKEN with privileges to create Build triggers")
var gitlabURL = flag.String("url", "", "GitLab instance address")

func doJsonRequest(method, urlStr string, bodyType string, body io.Reader, data interface{}) (resp *http.Response, err error) {
	if *privateToken == "" {
		return nil, errors.New("missing --private-token")
	}

	req, err := http.NewRequest(method, urlStr, body)
	if err != nil {
		return
	}

	req.Header.Set("Private-Token", *privateToken)
	if bodyType != "" {
		req.Header.Set("Content-Type", bodyType)
	}

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer io.Copy(ioutil.Discard, resp.Body)
	defer resp.Body.Close()

	if resp.StatusCode/100 == 2 {
		d := json.NewDecoder(resp.Body)
		err = d.Decode(data)
	} else {
		body, _ := ioutil.ReadAll(resp.Body)
		err = errors.New(resp.Status + " " + fmt.Sprintf("%s", body))
	}
	return
}

func getCommit(projectID int64, commitID string) (commit commit, err error) {
	reqURL := fmt.Sprintf("%s/api/v4/projects/%d/repository/commits/%s", *gitlabURL, projectID, commitID)
	_, err = doJsonRequest("GET", reqURL, "", nil, &commit)
	return
}

func listTokens(projectID int64) (tokens []tokenResponse, err error) {
	reqURL := fmt.Sprintf("%s/api/v4/projects/%d/triggers", *gitlabURL, projectID)
	_, err = doJsonRequest("GET", reqURL, "", nil, &tokens)
	return
}

func createToken(projectID int64) (token tokenResponse, err error) {
	var jsonStr = []byte(`{ "description": "MR trigger (created automatically)" }`)

	reqURL := fmt.Sprintf("%s/api/v4/projects/%d/triggers", *gitlabURL, projectID)
	_, err = doJsonRequest("POST", reqURL, "application/json", bytes.NewBuffer(jsonStr), &token)
	return
}

func getTriggerToken(projectID int64) (string, error) {
	if *triggerToken != "" {
		return *triggerToken, nil
	}

	if tokens, err := listTokens(projectID); err == nil {
		for _, token := range tokens {
			if token.DeletedAt != "" || token.Token == "" {
				continue
			}
			log.Println("[TOKEN]", "found existing - id:", token.ID, ", description:", token.Description)
			return token.Token, nil
		}
	}

	if token, err := createToken(projectID); err == nil {
		log.Println("[TOKEN]", "created - id:", token.ID)
		return token.Token, nil
	} else {
		return "", err
	}
}

func runTrigger(projectID int64, values url.Values) (resp *http.Response, err error) {
	reqURL := fmt.Sprintf("%s/api/v4/projects/%d/trigger/pipeline", *gitlabURL, projectID)
	return http.PostForm(reqURL, values)
}

func getPendingBuilds(projectID int64, pipelineID int) (jobs []job, err error) {
	reqURL := fmt.Sprintf("%s/api/v4/projects/%d/pipelines/%d/jobs?scope[]=pending", *gitlabURL, projectID, pipelineID)
	_, err = doJsonRequest("GET", reqURL, "", nil, &jobs)
	return
}

func cancelBuild(projectID int64, buildID int) (job job, err error) {
	reqURL := fmt.Sprintf("%s/api/v4/projects/%d/jobs/%d/cancel", *gitlabURL, projectID, buildID)
	_, err = doJsonRequest("POST", reqURL, "", nil, &job)
	return
}

func getPipelines(projectID int64, ref string) (pipelines []pipeline, err error) {
	reqURL := fmt.Sprintf("%s/api/v4/projects/%d/pipelines?ref=%s&status=running&sort=asc", *gitlabURL, projectID, ref)
	_, err = doJsonRequest("GET", reqURL, "", nil, &pipelines)
	return
}

func cancelRedundantBuilds(projectID int64, ref string, excludePipeline int) {
	pipelines, err := getPipelines(projectID, ref)
	if err != nil {
		log.Println("ERROR", err)
	}

	for _, p := range pipelines {
		if p.ID == excludePipeline {
			continue
		}
		builds, err := getPendingBuilds(projectID, p.ID)
		if err != nil {
			log.Println("ERROR", err)
		}
		for _, b := range builds {
			log.Println("[BUILD] In pipeline", p.ID, "cancelling build:", b.ID, "(", b.Name, ")")
			_, err := cancelBuild(projectID, b.ID)
			if err != nil {
				log.Println("ERROR", err)
			}
		}
	}
}

func httpError(w http.ResponseWriter, r *http.Request, error string, code int) {
	http.Error(w, error, code)
	log.Println("[RESPONSE]", code, ":", error)
}

func webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		httpError(w, r, "we support POST method only, but it was:"+r.Method, http.StatusMethodNotAllowed)
		return
	}

	var webhook webhookRequest
	err := json.NewDecoder(r.Body).Decode(&webhook)
	if err != nil {
		httpError(w, r, "error decoding json body of request:"+err.Error(), http.StatusUnsupportedMediaType)
		return
	}

	if webhook.ObjectKind != "merge_request" {
		httpError(w, r, "we support merge_request objects only, but it was:"+webhook.ObjectKind, http.StatusUnprocessableEntity)
		return
	}

	log.Println("[MR]",
		"state:", webhook.Attributes.State,
		"id:", webhook.Attributes.ID,
		"iid:", webhook.Attributes.IID,
		"action:", webhook.Attributes.Action,
		"project:", webhook.Attributes.Source.HTTPURL,
		"branches:", webhook.Attributes.SourceBranch, ">", webhook.Attributes.TargetBranch,
		"commit:", webhook.Attributes.LastCommit.ID, "@", webhook.Attributes.LastCommit.Timestamp,
		"wip:", webhook.Attributes.WorkInProgress,
		"merge_status:", webhook.Attributes.MergeStatus)

	if webhook.Attributes.Action != "open" && webhook.Attributes.Action != "reopen" && webhook.Attributes.Action != "update" {
		httpError(w, r, "ignored MR action - "+webhook.Attributes.Action, http.StatusNonAuthoritativeInfo)
		return
	}

	if !strings.HasPrefix(webhook.Attributes.Source.HTTPURL, *gitlabURL) {
		httpError(w, r, webhook.Attributes.Source.HTTPURL+"is not a prefix of"+*gitlabURL, http.StatusNotFound)
		return
	}

	commit, err := getCommit(webhook.Attributes.SourceProjectID, webhook.Attributes.LastCommit.ID)
	if err != nil {
		httpError(w, r, "error getting details of the commit:"+err.Error(), http.StatusInternalServerError)
		return
	}
	if commit.LastPipeline != nil {
		message := fmt.Sprintf("commit: %s already has associated pipeline: %d", webhook.Attributes.LastCommit.ID, commit.LastPipeline.ID)
		httpError(w, r, message, http.StatusOK)
		defer cancelRedundantBuilds(webhook.Attributes.SourceProjectID, webhook.Attributes.SourceBranch, commit.LastPipeline.ID)
		return
	}

	token, err := getTriggerToken(webhook.Attributes.SourceProjectID)
	if err != nil {
		httpError(w, r, "error getting trigger token - "+err.Error(), http.StatusInternalServerError)
		return
	}

	values := make(url.Values)
	values.Set("token", token)
	values.Set("ref", webhook.Attributes.SourceBranch)
	values.Set("variables[CI_MERGE_REQUEST]", "true")

	resp, err := runTrigger(webhook.Attributes.SourceProjectID, values)
	if err != nil {
		httpError(w, r, "error triggering pipeline - "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	var p pipeline
	err = json.NewDecoder(resp.Body).Decode(&p)
	if err != nil {
		httpError(w, r, "error decoding json body of pipeline creation response:"+err.Error(), http.StatusUnsupportedMediaType)
		return
	}

	message := fmt.Sprintf("created pipeline id: %d", p.ID)
	httpError(w, r, message, resp.StatusCode)
	defer cancelRedundantBuilds(webhook.Attributes.SourceProjectID, webhook.Attributes.SourceBranch, p.ID)
	return
}

func main() {
	flag.Parse()

	if *triggerToken == "" && *privateToken == "" ||
		*triggerToken != "" && *privateToken != "" {
		log.Fatal("Specify --trigger-token or --private-token")
	}

	if *gitlabURL == "" {
		log.Fatal("Specify --url an address of GitLab instance")
	}

	println("Listening on", *listenAddr, "...")

	http.HandleFunc("/webhook.json", webhookHandler)
	log.Fatal(http.ListenAndServe(*listenAddr, nil))
}
