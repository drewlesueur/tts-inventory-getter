package scrape

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"os/exec"
	"syscall"
	"time"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

//go:embed nhtsa.js
var nhtsaScript string

// NHTSALookup reuses the installed Playwright library through the existing
// browser adapter. Deliberately does not use inventory's stealth/fallback paths.
func (b *PlaywrightBrowser) NHTSALookup(ctx context.Context, vin string, date time.Time) ([]byte, *model.NHTSAError) {
	release, err := AcquireBrowserSlot(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &model.NHTSAError{Code: "LOOKUP_TIMEOUT", Message: "Lookup timed out or worker stopped"}
		}
		return nil, &model.NHTSAError{Code: "LOOKUP_FAILED", Message: "Browser slot unavailable"}
	}
	cmd := exec.CommandContext(ctx, "node", "-e", nhtsaScript+"\nlookup(process.argv[1],process.argv[2]).then(({pdf})=>process.stdout.write(JSON.stringify({pdf:pdf.toString('base64')}))).catch(e=>{process.stdout.write(JSON.stringify({error:{code:e.code||'LOOKUP_FAILED',message:e.message.slice(0,1000)}}));process.exitCode=1;});", vin, date.UTC().Format(time.RFC3339))
	manageChildProcess(cmd)
	// SIGTERM lets the adapter close Chromium. WaitDelay bounds cleanup if Node hangs.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	var output bytes.Buffer
	cmd.Stdout = &output
	err = cmd.Run()
	clean := browserCommandClean(ctx, cmd, err)
	release(clean)
	if ctx.Err() != nil {
		return nil, &model.NHTSAError{Code: "LOOKUP_TIMEOUT", Message: "Lookup timed out or worker stopped"}
	}
	if !clean {
		return nil, &model.NHTSAError{Code: "LOOKUP_FAILED", Message: "Could not verify browser cleanup"}
	}
	var result struct {
		PDF   []byte            `json:"pdf"`
		Error *model.NHTSAError `json:"error"`
	}
	if json.Unmarshal(output.Bytes(), &result) != nil {
		return nil, &model.NHTSAError{Code: "LOOKUP_FAILED", Message: "Browser worker failed to return a valid result"}
	}
	if result.Error != nil {
		return nil, result.Error
	}
	if err != nil || !bytes.HasPrefix(result.PDF, []byte("%PDF-")) || len(result.PDF) >= model.NHTSAMaxPDF {
		return nil, &model.NHTSAError{Code: "PDF_FAILED", Message: "Browser did not return a valid PDF below 20 MB"}
	}
	return result.PDF, nil
}
