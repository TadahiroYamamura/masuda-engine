package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	if err := e.record(run, o, result{Outcome: OutcomeAnswered, Outputs: []string{name}}); err != nil {
		return err
	}
	e.log(run, Event{Kind: "answer", Occurrence: occ, Workflow: o.Workflow, Node: o.Node, Outcome: OutcomeAnswered})
	return nil
}
