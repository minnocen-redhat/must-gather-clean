#!/bin/bash

set -e

go run ./cmd/jsonschema -p schema pkg/schema/schema.json >pkg/schema/schema.go
