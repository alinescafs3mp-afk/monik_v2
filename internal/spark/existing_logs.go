package spark

const existingLogRoot = "/var/log/spark-model-collection-v4"
const existingLogRedactionFile = "/etc/monik-spark/existing-log-redactions.values"

// These are LOCAL approval values. The browser can select only an already
// approved model ID; no remote path, unit or glob is accepted.
func validExistingLogApproval(p Profile) bool {
	a := p.Target.Existing
	if a == nil {
		return false
	}
	if !p.LogEnabled {
		return a.LogRoot == "" && a.RedactionFile == "" && a.RedactionSHA256 == ""
	}
	return a.LogRoot == existingLogRoot && a.RedactionFile == existingLogRedactionFile && bareHash(a.RedactionSHA256)
}
