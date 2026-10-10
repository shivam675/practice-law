-- A reasoning model spends its token budget thinking before it answers, which
-- makes a tier's max_tokens mean something different per model. reasoning_effort
-- is the OpenAI-compatible knob for it: Ollama, vLLM and the hosted providers
-- all read the same field, and an empty value sends nothing so the provider
-- keeps its own default.
ALTER TABLE model_bindings
    ADD COLUMN reasoning_effort text NOT NULL DEFAULT ''
        CHECK (reasoning_effort IN ('', 'none', 'low', 'medium', 'high'));
