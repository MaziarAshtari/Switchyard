-- Vision bridge: images sent to a chat model are first described by another chat model.

ALTER TABLE models ADD COLUMN vision_bridge_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE models ADD COLUMN vision_bridge_model_id INTEGER REFERENCES models (id) ON DELETE SET NULL;
-- '' means the default prompt.
ALTER TABLE models ADD COLUMN vision_bridge_prompt TEXT NOT NULL DEFAULT '';
