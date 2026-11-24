// SPDX-License-Identifier: MPL-2.0

package memory

import "regexp"

var secretPattern = regexp.MustCompile(`(?i)\b(?:mot de passe|password|passwd|mdp|code pin|pin code|cvv|cvc|api[ _-]?key|token|secret)\b\s*(?:est|is|:|=)|\b(?:sk|ghp|gho|xox[bap]|AKIA)[-_A-Za-z0-9]{12,}|\b(?:\d[ -]?){13,19}\b|\b[A-Z]{2}\d{2}(?: ?[A-Z0-9]{4}){3,7}`)

// secret says whether a text looks like it holds a secret, which memory
// never keeps as a fact.
func secret(text string) bool { return secretPattern.MatchString(text) }
