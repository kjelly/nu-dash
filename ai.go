package main

import (
	"context"
	"fmt"
	"github.com/google/generative-ai-go/genai"
	"github.com/xyproto/ollamaclient"
	"google.golang.org/api/option"
	"log"
	"os"
	"strings"
)

func chat(prompt string, model string) string {
	oc := ollamaclient.NewWithModel(model)

	oc.Verbose = true

	if err := oc.PullIfNeeded(); err != nil {
		return err.Error()
	}

	output, err := oc.GetOutput(prompt)
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("\n%s\n", strings.TrimSpace(output))
}
func gemini(prompt string, modelName string) string {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(os.Getenv("GEMINI_API_KEY")))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	model := client.GenerativeModel(modelName)
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		log.Fatal(err)
	}
	return responseToString(resp)

}

func responseToString(resp *genai.GenerateContentResponse) string {
	var ret string
	for _, cand := range resp.Candidates {
		for _, part := range cand.Content.Parts {
			ret = ret + fmt.Sprintf("%v", part)
		}
	}
	return ret + "\n---"
}
