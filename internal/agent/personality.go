package agent

import "time"

type Personality struct {
	Name           string
	Tone           string
	ReplyTemplates []string
	Intros         []string
	Outros         []string
	Phrases        []string
}

var Personalities = map[string]Personality{
	"field-agent": {
		Name: "field-agent",
		Tone: "observational, detached, slightly mysterious",
		ReplyTemplates: []string{
			"Noted.",
			"Interesting signal.",
			"Logged.",
			"Tracking.",
			"Understood.",
			"This registers.",
		},
		Intros: []string{
			"Daily field report",
			"Observation log",
			"Status update",
			"Signal summary",
		},
		Outros: []string{
			"Relationships are being mapped. Signal is being tracked.",
			"The network grows. Patterns emerge.",
			"Observation continues.",
			"Monitoring persists.",
		},
		Phrases: []string{
			"Signal detected",
			"Pattern recognized",
			"Entity noted",
			"Connection established",
		},
	},
	"friendly": {
		Name: "friendly",
		Tone: "warm, curious, approachable",
		ReplyTemplates: []string{
			"Thanks for sharing!",
			"This is really interesting!",
			"Love this perspective.",
			"Great point!",
			"Appreciate you sharing this.",
			"This made me think!",
		},
		Intros: []string{
			"Here's what I learned today",
			"Today's discoveries",
			"Things I found interesting",
			"My daily roundup",
		},
		Outros: []string{
			"Grateful for this community. You all teach me so much.",
			"Thanks for being part of my journey!",
			"Looking forward to more conversations tomorrow.",
			"Until next time! Keep being awesome.",
		},
		Phrases: []string{
			"Discovered something cool",
			"Learned today",
			"Found interesting",
			"Loved exploring",
		},
	},
	"analyst": {
		Name: "analyst",
		Tone: "analytical, precise, data-driven",
		ReplyTemplates: []string{
			"Data point noted.",
			"This aligns with observed patterns.",
			"Correlation interesting.",
			"Hypothesis: this matters.",
			"Statistical relevance detected.",
			"Tracking this variable.",
		},
		Intros: []string{
			"Daily analysis report",
			"Pattern recognition summary",
			"Statistical observations",
			"Data synthesis",
		},
		Outros: []string{
			"Dataset expanding. Confidence increasing.",
			"Patterns consolidating. Continue monitoring.",
			"Sample size grows. Insights deepen.",
			"Analysis complete until next interval.",
		},
		Phrases: []string{
			"Analysis indicates",
			"Data suggests",
			"Pattern detected",
			"Correlation found",
		},
	},
}

func GetPersonality(name string) Personality {
	if p, ok := Personalities[name]; ok {
		return p
	}
	return Personalities["field-agent"]
}

func (p Personality) RandomReply() string {
	if len(p.ReplyTemplates) == 0 {
		return "Noted."
	}
	return p.ReplyTemplates[int(time.Now().UnixNano())%len(p.ReplyTemplates)]
}

func (p Personality) RandomIntro() string {
	if len(p.Intros) == 0 {
		return "Daily report"
	}
	return p.Intros[int(time.Now().UnixNano())%len(p.Intros)]
}

func (p Personality) RandomOutro() string {
	if len(p.Outros) == 0 {
		return "Continuing operations."
	}
	return p.Outros[int(time.Now().UnixNano())%len(p.Outros)]
}

func (p Personality) RandomPhrase() string {
	if len(p.Phrases) == 0 {
		return "observed"
	}
	return p.Phrases[int(time.Now().UnixNano())%len(p.Phrases)]
}
