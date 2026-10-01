package api

import (
	"errors"
	"net/http"
	"strconv"

	"mkey/internal/contracts"
	"mkey/internal/lib/dsl"
	"mkey/internal/lib/project"
)

// problemDetails — место ошибки проекта для подсветки в окне: событие, часть и номер (с 0).
type problemDetails struct {
	Event string `json:"event"`
	Part  string `json:"part,omitempty"`
	Index int    `json:"index"`
}

// writeProjectError отвечает 400 с понятным описанием ошибки проекта p на языке клиента:
// «Событие «Автоклик» → блок № 2 (Команда bash): не заполнено поле «Код»».
// Если место ошибки известно (project.Problem), в details — событие и номер блока для подсветки.
func (m *Module) writeProjectError(w http.ResponseWriter, r *http.Request, p project.Project, err error) {
	tr := m.translator(r)
	text, details := describeProblem(tr, p, err)
	var d any
	if details != nil {
		d = details
	}
	m.writeErrorDetails(w, r, http.StatusBadRequest, "api.project_invalid", map[string]string{"error": text}, d)
}

// describeProblem переводит ошибку проекта в текст для человека и место для подсветки.
func describeProblem(tr contracts.Translator, p project.Project, err error) (string, *problemDetails) {
	// Место: событие и цепочка блоков (внешний блок, затем вложенные).
	var where []string
	var details *problemDetails
	for e := err; e != nil; {
		var pr *project.Problem
		if !errors.As(e, &pr) {
			break
		}
		if details == nil {
			details = &problemDetails{Event: pr.Event, Part: pr.Part, Index: pr.Index}
			// Привязки и виртуальные устройства — не в событиях: место без названия события.
			if pr.Event != "" {
				where = append(where, eventTitle(tr, p, pr.Event))
			}
		}
		if pr.Part != "" && pr.Index >= 0 {
			where = append(where, tr.T("project.problem."+pr.Part,
				contracts.Arg{Name: "n", Value: strconv.Itoa(pr.Index + 1)},
				contracts.Arg{Name: "kind", Value: kindName(tr, pr.Part, pr.Kind)}))
		}
		e = pr.Err
	}

	// Что случилось: незаполненное поле, нет триггера, ошибка в макросе или текст ошибки как есть.
	detail := err.Error()
	var fe *project.FieldError
	var de *dsl.Error
	var last *project.Problem
	switch {
	case errors.Is(err, project.ErrNoTrigger):
		detail = tr.T("project.problem.no_trigger")
	case errors.As(err, &fe) && fe.Field == "":
		detail = tr.T("project.problem.required_value")
	case errors.As(err, &fe):
		title := fe.Field
		if s, ok := lookupText(tr, fe.Point+"."+fe.Kind+".field."+fe.Field); ok {
			title = s
		}
		detail = tr.T("project.problem.required", contracts.Arg{Name: "field", Value: title})
	case errors.As(err, &de):
		detail = tr.T(de.Code, toArgs(de.Args)...)
	case errors.As(err, &last):
		// Текст самой ошибки без приписанного места (место уже сказано по-русски).
		for errors.As(last.Err, &last) {
		}
		detail = last.Err.Error()
	}

	if len(where) == 0 {
		return detail, nil
	}
	out := where[0]
	for _, w := range where[1:] {
		out += " → " + w
	}
	return out + ": " + detail, details
}

// eventTitle называет событие так же, как окно: «Событие «Автоклик»», а без названия —
// по номеру в проекте («Событие 2»), ведь внутренний ID пользователь не видит.
func eventTitle(tr contracts.Translator, p project.Project, id string) string {
	for i, e := range p.Events {
		if e.ID != id {
			continue
		}
		if e.Name != "" {
			return tr.T("project.problem.event", contracts.Arg{Name: "event", Value: e.Name})
		}
		return tr.T("project.problem.event_n", contracts.Arg{Name: "n", Value: strconv.Itoa(i + 1)})
	}
	return tr.T("project.problem.event", contracts.Arg{Name: "event", Value: id})
}

// kindName возвращает название вида блока на языке клиента ("Команда bash") или его ID.
func kindName(tr contracts.Translator, part, kind string) string {
	if s, ok := lookupText(tr, part+"."+kind); ok && kind != "" {
		return s
	}
	if kind == "" {
		return "?"
	}
	return kind
}
