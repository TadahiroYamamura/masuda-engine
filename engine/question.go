package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// question opens a fixed-question node once and then waits for Answer. Like
// approval, the request is stored before it is announced so a repeated
// Advance hands out the same request.
func (m *mover) question(cur *occurrence, n *Node) (Status, bool, error) {
	var req QuestionRequest
	err := m.e.getJSON(m.run, prefQuestion+cur.ID, &req)
	if err == nil {
		return Status{Kind: StatusQuestion, Occurrence: cur.ID, Question: &req}, false, nil
	}
	if !errors.Is(err, errMissing) {
		return Status{}, false, err
	}
	if err := m.need(); err != nil {
		return Status{}, false, err
	}
	req = QuestionRequest{Run: m.run, Occurrence: cur.ID, Questions: n.Questions}
	applied, err := m.e.create(m.run, prefQuestion+cur.ID, req)
	if err != nil || !applied {
		return Status{}, true, err
	}
	if err := m.e.runner.OpenQuestion(m.ctx, req); err != nil {
		return Status{}, false, err
	}
	m.e.log(m.run, Event{Kind: "question-open", Occurrence: cur.ID, Workflow: cur.Workflow, Node: cur.Node})
	return Status{Kind: StatusQuestion, Occurrence: cur.ID, Question: &req}, false, nil
}

// answer stores the answers as the node's single output (JSON, id->answer)
// and finishes the occurrence as answered. An answer that does not fit the
// questions is refused without recording, so the human can answer again.
func (e *Engine) answer(ctx context.Context, run RunID, occ string, a Answer) error {
	if st, err := e.walk(ctx, run, false); err == nil && st.Kind == StatusAgent && st.Occurrence == occ {
		var o occurrence
		if err := e.getJSON(run, prefOcc+occ, &o); err != nil {
			return err
		}
		if n, err := e.nodeOf(&o); err != nil {
			return err
		} else if n.Type == NodeQuestion {
			return e.collectAnswer(run, &o, a)
		}
	}
	st, o, err := e.waiting(ctx, run, occ, StatusQuestion)
	if err != nil {
		return err
	}
	n, err := e.nodeOf(o)
	if err != nil {
		return err
	}
	asked := map[string]Question{}
	for _, q := range st.Question.Questions {
		asked[q.ID] = q
		got, ok := a.Answers[q.ID]
		if !ok {
			return fmt.Errorf("engine: question %s: no answer to %q", occ, q.ID)
		}
		if len(q.Options) > 0 && !slices.Contains(q.Options, got) {
			return fmt.Errorf("engine: question %s: %q is not one of %v for %q", occ, got, q.Options, q.ID)
		}
	}
	for id := range a.Answers {
		if _, ok := asked[id]; !ok {
			return fmt.Errorf("engine: question %s: %q was not asked", occ, id)
		}
	}
	name := n.Outputs[0]
	content, err := json.Marshal(a.Answers)
	if err != nil {
		return err
	}
	if err := e.validateData(name, content); err != nil {
		return fmt.Errorf("engine: question %s: answers do not fit %s: %w", occ, name, err)
	}
	if err := e.runner.PutData(ctx, run, DataRef{Name: name, Occurrence: occ}, content); err != nil {
		return err
	}
	if err := e.record(run, o, result{Outcome: OutcomeAnswered, Outputs: []string{name}}, ""); err != nil {
		return err
	}
	e.log(run, Event{Kind: "answer", Occurrence: occ, Workflow: o.Workflow, Node: o.Node, Outcome: OutcomeAnswered})
	return nil
}

const prefAnswer = "answer/"

// collectAnswer keeps one ask_human answer of a role question's task. Each
// answer is its own record rather than a merged value, so two answers
// arriving at once cannot overwrite each other; they are merged in order
// when the agent reports done.
func (e *Engine) collectAnswer(run RunID, o *occurrence, a Answer) error {
	for {
		kvs, err := e.store.List(runKey(run, prefAnswer+o.ID+"/"))
		if err != nil {
			return err
		}
		applied, err := e.create(run, prefAnswer+devKey(o.ID, len(kvs)+1), a)
		if err != nil {
			return err
		}
		if applied {
			e.log(run, Event{Kind: "answer", Occurrence: o.ID, Workflow: o.Workflow, Node: o.Node})
			return nil
		}
	}
}

// collectedAnswers merges the answers collected for occ (later ones win)
// into the JSON stored as name. reason is non-empty when that cannot be
// accepted as the node's result.
func (e *Engine) collectedAnswers(run RunID, occ, name string) (content []byte, reason string, err error) {
	kvs, err := e.store.List(runKey(run, prefAnswer+occ+"/"))
	if err != nil {
		return nil, "", err
	}
	slices.SortFunc(kvs, func(a, b KV) int { return strings.Compare(a.Key, b.Key) })
	merged := map[string]string{}
	for _, kv := range kvs {
		var a Answer
		if err := json.Unmarshal(kv.Value, &a); err != nil {
			return nil, "", fmt.Errorf("%s: %w", kv.Key, err)
		}
		for k, v := range a.Answers {
			merged[k] = v
		}
	}
	if len(merged) == 0 {
		return nil, "人間の答えが1つも無い（ask_humanで答えを得てから完了を報告する）", nil
	}
	if content, err = json.Marshal(merged); err != nil {
		return nil, "", err
	}
	if err := e.validateData(name, content); err != nil {
		return nil, fmt.Sprintf("集めた答えが%sとして不正: %v", name, err), nil
	}
	return content, "", nil
}
