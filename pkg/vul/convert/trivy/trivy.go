// Copyright 2023 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package trivy

import (
	"fmt"

	"github.com/GoogleCloudPlatform/aactl/pkg/types"
	"github.com/GoogleCloudPlatform/aactl/pkg/utils"
	"github.com/Jeffail/gabs/v2"
	"github.com/pkg/errors"
	g "google.golang.org/genproto/googleapis/grafeas/v1"
)

// Convert converts Trivy JSON to Grafeas Note/Occurrence format.
func Convert(s *utils.Source) (types.NoteOccurrencesMap, error) {
	if s == nil || s.Data == nil {
		return nil, types.ErrInvalidSource
	}

	if !s.Data.Search("Results").Exists() {
		return nil, errors.New("unable to find Results in source data")
	}

	list := make(types.NoteOccurrencesMap, 0)

	for _, r := range s.Data.Search("Results").Children() {
		for _, v := range r.Search("Vulnerabilities").Children() {
			// create note
			n := convertNote(s, v)

			// don't add notes with no CVSS score
			if n == nil || n.GetVulnerability().CvssScore == 0 {
				continue
			}
			noteID := utils.GetPrefixNoteName(n.GetShortDescription())

			// If cve is not found, add to map
			if _, ok := list[noteID]; !ok {
				list[noteID] = types.NoteOccurrences{Note: n}
			}
			nocc := list[noteID]
			occ := convertOccurrence(s, v, noteID, getPackageType(r))
			nocc.Occurrences = append(nocc.Occurrences, occ)
			list[noteID] = nocc
		}
	}

	return list, nil
}

func convertNote(s *utils.Source, v *gabs.Container) *g.Note {
	cve := v.Search("VulnerabilityID").Data().(string)

	if v.Search("CVSS", "nvd").Data() == nil {
		return nil
	}
	nvd := v.Search("CVSS", "nvd")

	n := g.Note{
		ShortDescription: cve,
		RelatedUrl: []*g.RelatedUrl{
			{
				Label: "Registry",
				Url:   s.URI,
			},
			{
				Label: "PrimaryURL",
				Url:   v.Search("PrimaryURL").Data().(string),
			},
		},
		Type: &g.Note_Vulnerability{
			Vulnerability: &g.VulnerabilityNote{
				// Details in Notes are not populated since we will never see the full list
				Details: []*g.VulnerabilityNote_Detail{
					{
						AffectedCpeUri:  "N/A",
						AffectedPackage: "N/A",
					},
				},
				Severity:         utils.ToGrafeasSeverity(v.Search("Severity").Data().(string)),
				SourceUpdateTime: utils.ToGRPCTime(v.Search("LastModifiedDate").Data()),
			},
		},
	} // end note

	// CVSSv2
	if nvd.Search("V2Vector").Data() != nil {
		n.LongDescription = nvd.Search("V2Vector").Data().(string)
		n.GetVulnerability().CvssVersion = g.CVSSVersion_CVSS_VERSION_2
		n.GetVulnerability().CvssScore = utils.ToFloat32(nvd.Search("V2Score").Data())
	}

	// CVSSv3, will override v2 values
	if nvd.Search("V3Vector").Data() != nil {
		n.LongDescription = nvd.Search("V3Vector").Data().(string)
		n.GetVulnerability().CvssVersion = g.CVSSVersion_CVSS_VERSION_3
		n.GetVulnerability().CvssScore = utils.ToFloat32(nvd.Search("V3Score").Data())
		n.GetVulnerability().CvssV3 = utils.ToCVSSv3(
			utils.ToFloat32(nvd.Search("V3Score").Data()),
			nvd.Search("V3Vector").Data().(string),
		)
	}

	// References
	for _, r := range v.Search("References").Children() {
		n.RelatedUrl = append(n.RelatedUrl, &g.RelatedUrl{
			Url:   r.Data().(string),
			Label: "Url",
		})
	}

	return &n
}

// convertOccurrence converts Trivy JSON to Grafeas Occurrence format.
func convertOccurrence(s *utils.Source, v *gabs.Container, noteID string, packageType string) *g.Occurrence {
	cve := v.Search("VulnerabilityID").Data().(string)
	noteName := fmt.Sprintf("projects/%s/notes/%s", s.Project, noteID)

	if v.Search("CVSS", "nvd").Data() == nil {
		return nil
	}
	nvd := v.Search("CVSS", "nvd")

	// Create Occurrence
	o := g.Occurrence{
		ResourceUri: fmt.Sprintf("https://%s", s.URI),
		NoteName:    noteName,
		Details: &g.Occurrence_Vulnerability{
			Vulnerability: &g.VulnerabilityOccurrence{
				ShortDescription: cve,
				RelatedUrls: []*g.RelatedUrl{
					{
						Label: "Registry",
						Url:   s.URI,
					},
					{
						Label: "PrimaryURL",
						Url:   v.Search("PrimaryURL").Data().(string),
					},
				},
				CvssScore: utils.ToFloat32(v.Search("CVSS", "nvd", "V2Score").Data()),
				Severity:  utils.ToGrafeasSeverity(v.Search("Severity").Data().(string)),
				// TODO: What is the difference between severity and effective severity?
				EffectiveSeverity: utils.ToGrafeasSeverity(v.Search("Severity").Data().(string)),
			}},
	}

	// PackageIssues
	o.GetVulnerability().PackageIssue = append(
		o.GetVulnerability().PackageIssue,
		getBasePackageIssue(v, packageType))

	// CVSSv2
	if nvd.Search("V2Vector").Data() != nil {
		o.GetVulnerability().LongDescription = nvd.Search("V2Vector").Data().(string)
		o.GetVulnerability().CvssVersion = g.CVSSVersion_CVSS_VERSION_2
		o.GetVulnerability().CvssScore = utils.ToFloat32(nvd.Search("V2Score").Data())
	}

	// CVSSv3, will override v2 values
	if nvd.Search("V3Vector").Data() != nil {
		o.GetVulnerability().LongDescription = nvd.Search("V3Vector").Data().(string)
		o.GetVulnerability().CvssVersion = g.CVSSVersion_CVSS_VERSION_3
		o.GetVulnerability().CvssScore = utils.ToFloat32(nvd.Search("V3Score").Data())
		o.GetVulnerability().Cvssv3 = utils.ToCVSS(
			utils.ToFloat32(nvd.Search("V3Score").Data()),
			nvd.Search("V3Vector").Data().(string),
		)
	}

	// References
	for _, r := range v.Search("References").Children() {
		o.GetVulnerability().RelatedUrls = append(o.GetVulnerability().RelatedUrls, &g.RelatedUrl{
			Url:   r.Data().(string),
			Label: "Url",
		})
	}

	return &o
}

// makeCPE creates CPE from Snyk data as the OSS CLI does not generate CPEs
// NOTE: This is for demo purposes only and is not a complete CPE generator
// Ref: https://en.wikipedia.org/wiki/Common_Platform_Enumeration
// Schema: cpe:2.3:a:<vendor>:<product>:<version>:<update>:<edition>:<language>:<sw_edition>:<target_sw>:<target_hw>:<other>
func makeCPE(v *gabs.Container) string {
	src := v.Search("SeveritySource").Data().(string)
	pkgName := v.Search("PkgName").Data().(string)
	pkgVersion := v.Search("InstalledVersion").Data().(string)

	return fmt.Sprintf("cpe:2.3:a:%s:%s:%s:*:*:*:*:*:*:*",
		src,
		pkgName,
		pkgVersion)
}

// getPackageType returns the package type based on the Trivy Class/Type combination.
// If the Class is not lang-pkgs, then it is an OS package.
// Else, use utils.ParsePackageType to determine the package type.
func getPackageType(r *gabs.Container) string {
	if r.Search("Class").Data().(string) != "lang-pkgs" {
		return "OS"
	}

	return utils.ParsePackageType(r.Search("Type").Data().(string))
}

func getBasePackageIssue(v *gabs.Container, packageType string) *g.VulnerabilityOccurrence_PackageIssue {
	return &g.VulnerabilityOccurrence_PackageIssue{
		PackageType:     packageType,
		AffectedCpeUri:  makeCPE(v),
		AffectedPackage: v.Search("PkgName").Data().(string),
		AffectedVersion: &g.Version{
			Name: v.Search("InstalledVersion").Data().(string),
			Kind: g.Version_NORMAL,
		},
		FixedCpeUri:  makeCPE(v),
		FixedPackage: v.Search("PkgName").Data().(string),
		FixedVersion: &g.Version{
			Kind: g.Version_MAXIMUM,
		},
	}
}
