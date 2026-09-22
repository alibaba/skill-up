# Skill observation contract

This directory owns the host-neutral persisted observation contract used by
skill-up host plugins.

- `v1alpha1/observation.schema.json` defines the normalized record.
- `v1alpha1/fixtures/` contains cross-host conformance examples.

Host adapters remain independent because their lifecycle events and runtime
languages differ. Each adapter must normalize into this contract and pass the
same fixtures. Release packaging may copy the schema into a host plugin, but
this directory is the only tracked source.
