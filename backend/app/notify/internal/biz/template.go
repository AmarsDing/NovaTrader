package biz

import (
	"strconv"
	"strings"
)

const defaultTemplate = "{{title}}\n{{body}}"

// Render 替换 {{name}}。未知占位符保留。正文为空时去掉多余空行。
func Render(tmpl string, msg Message) string {
	if strings.TrimSpace(tmpl) == "" {
		tmpl = defaultTemplate
	}
	fields := map[string]string{}
	for k, v := range msg.Fields {
		fields[k] = v
	}
	fields["title"] = msg.Title
	fields["body"] = msg.Body
	fields["category"] = msg.Category
	fields["priority"] = msg.Priority
	fields["count"] = strconv.Itoa(msg.MergeCount + 1)
	out := tmpl
	for k, v := range fields {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return clip(strings.TrimSpace(out), 4000)
}

func (c Config) template(category string) string {
	if c.Templates != nil {
		if t := strings.TrimSpace(c.Templates[category]); t != "" {
			return t
		}
	}
	return defaultTemplate
}
