package llm

import "testing"

func TestNormalizeBaseAcceptsEitherForm(t *testing.T) {
	cases := map[string]string{
		"https://ollama.example.com":          "https://ollama.example.com/v1",
		"https://ollama.example.com/":         "https://ollama.example.com/v1",
		"https://ollama.example.com/v1":       "https://ollama.example.com/v1",
		"https://ollama.example.com/v1/":      "https://ollama.example.com/v1",
		" http://host.docker.internal:11434 ": "http://host.docker.internal:11434/v1",
	}
	for in, want := range cases {
		if got := normalizeBase(in); got != want {
			t.Errorf("normalizeBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSummarisePrefersTheProviderMessage(t *testing.T) {
	got := summarise([]byte(`{"error":{"message":"model 'qwen3:8b' not found"}}`))
	if got != "model 'qwen3:8b' not found" {
		t.Fatalf("summarise = %q", got)
	}
	if summarise([]byte("<html>502 Bad Gateway</html>")) != "<html>502 Bad Gateway</html>" {
		t.Fatal("a non-JSON body should survive as-is")
	}
}
