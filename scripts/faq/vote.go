package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Votes (Ross, 2026-11-23): a thumbs up or down on an answer, kept in the
// question log as a line of its own that names the answer's id, so the
// log stays append-only and review.sh joins the two. Only an answer this
// process gave out can be voted on, and only a few times (a visitor may
// change their mind), so a vote line is never an arbitrary write.

const (
	maxBallots     = 1024 // answers remembered for voting; the oldest go first
	maxVotesPerAns = 4
)

type ballot struct {
	question string
	votes    int
}

type ballots struct {
	mu    sync.Mutex
	byID  map[string]*ballot
	order []string
}

// issue names a new answer to question and remembers it for voting.
func (b *ballots) issue(question string) string {
	var r [8]byte
	rand.Read(r[:])
	id := hex.EncodeToString(r[:])
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.byID == nil {
		b.byID = map[string]*ballot{}
	}
	if len(b.order) >= maxBallots {
		delete(b.byID, b.order[0])
		b.order = b.order[1:]
	}
	b.byID[id] = &ballot{question: question}
	b.order = append(b.order, id)
	return id
}

// cast counts a vote on id and returns the answer's question, or why not.
func (b *ballots) cast(id string) (string, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.byID[id]
	switch {
	case a == nil:
		return "", "that answer can no longer be voted on (the service has restarted since, or it is too old)"
	case a.votes >= maxVotesPerAns:
		return "", "that answer has had its votes"
	}
	a.votes++
	return a.question, ""
}

type voteRequest struct {
	ID   string `json:"id"`
	Vote string `json:"vote"` // up or down
}

// voteLine is a vote as the log keeps it: the answer's id and question,
// never the address.
type voteLine struct {
	Time     string `json:"time"`
	ID       string `json:"id"`
	Vote     string `json:"vote"`
	Question string `json:"question"`
	Public   bool   `json:"public,omitempty"`
}

func (s *server) vote(w http.ResponseWriter, r *http.Request) {
	var req voteRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "a vote is {id, vote}")
		return
	}
	if req.Vote != "up" && req.Vote != "down" {
		writeError(w, http.StatusBadRequest, "a vote is up or down")
		return
	}
	if s.logPath == "" {
		writeError(w, http.StatusNotFound, "votes are not kept here")
		return
	}
	q, why := s.votes.cast(strings.TrimSpace(req.ID))
	if why != "" {
		writeError(w, http.StatusNotFound, why)
		return
	}
	s.appendLog(voteLine{Time: time.Now().UTC().Format(time.RFC3339), ID: req.ID, Vote: req.Vote, Question: q, Public: s.public})
	writeJSON(w, http.StatusOK, map[string]string{"vote": req.Vote})
}
