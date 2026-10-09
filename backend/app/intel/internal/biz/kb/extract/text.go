package extract

import (
	"regexp"
	"strings"
)

var mdHeading = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)

// markdown 按 # 标题切章节，标题路径用 " > " 连接。代码块里的 # 不算标题。
func markdown(data []byte) ([]Section, error) {
	text, err := decodeText(data)
	if err != nil {
		return nil, err
	}
	var (
		out     []Section
		path    [6]string
		body    []string
		inFence bool
	)
	heading := func() string {
		var parts []string
		for _, p := range path {
			if p != "" {
				parts = append(parts, p)
			}
		}
		return strings.Join(parts, " > ")
	}
	current := ""
	flush := func() {
		if paras := splitParagraphs(strings.Join(body, "\n")); len(paras) > 0 {
			out = append(out, Section{Heading: current, Paragraphs: paras})
		}
		body = body[:0]
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			body = append(body, line)
			continue
		}
		if !inFence {
			if m := mdHeading.FindStringSubmatch(trimmed); m != nil {
				flush()
				level := len(m[1])
				path[level-1] = m[2]
				for i := level; i < len(path); i++ {
					path[i] = ""
				}
				current = heading()
				continue
			}
		}
		body = append(body, line)
	}
	flush()
	return out, nil
}

// plain 是纯文本：一个无标题章节，空行切段。
func plain(data []byte) ([]Section, error) {
	text, err := decodeText(data)
	if err != nil {
		return nil, err
	}
	return []Section{{Paragraphs: splitParagraphs(text)}}, nil
}
