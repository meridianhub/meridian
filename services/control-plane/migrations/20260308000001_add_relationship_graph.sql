-- Add relationship_graph JSONB column to manifest_version table.
-- Stores the relationship graph extracted during manifest validation,
-- enabling impact analysis queries without re-parsing the manifest.
ALTER TABLE manifest_version ADD COLUMN relationship_graph JSONB;
