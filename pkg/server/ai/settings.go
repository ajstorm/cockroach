// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package ai

import "github.com/cockroachdb/cockroach/pkg/settings"

// OpenAIAPIKey is the API key for OpenAI GPT models used by the AI Insights feature.
var OpenAIAPIKey = settings.RegisterStringSetting(
	settings.ApplicationLevel,
	"ai.openai_api_key",
	"API key for OpenAI GPT models used by the AI Insights feature in DB Console",
	"",
	settings.WithPublic,
	settings.WithReportable(false),
	settings.Sensitive,
)

// EnableAIInsights controls whether the AI Insights feature is enabled in DB Console.
var EnableAIInsights = settings.RegisterBoolSetting(
	settings.ApplicationLevel,
	"ai.insights.enabled",
	"enables or disables the AI Insights feature in DB Console",
	true,
	settings.WithPublic,
	settings.WithReportable(true),
)

// MaxIterations controls the maximum number of tool-calling iterations the AI agent can perform.
var MaxIterations = settings.RegisterIntSetting(
	settings.ApplicationLevel,
	"ai.max_iterations",
	"maximum number of tool-calling iterations the AI agent can perform before returning a response",
	30,
	settings.WithPublic,
	settings.WithReportable(true),
)
