package web

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/r3labs/diff/v2"
	uuid "github.com/satori/go.uuid"

	"github.com/whywaita/myshoes/pkg/datastore"
	"github.com/whywaita/myshoes/pkg/gh"
	"github.com/whywaita/myshoes/pkg/logger"

	"goji.io/pat"
)

// TargetCreateParam is parameter for POST /target
type TargetCreateParam struct {
	datastore.Target

	RunnerUser    *string `json:"runner_user"` // nullable
	GHEDomain     string  `json:"ghe_domain"`
	RunnerVersion *string `json:"runner_version"` // nullable
	ProviderURL   *string `json:"provider_url"`   // nullable
}

// UserTarget is format for user
type UserTarget struct {
	UUID              uuid.UUID              `json:"id"`
	Scope             string                 `json:"scope"`
	TokenExpiredAt    time.Time              `json:"token_expired_at"`
	GHEDomain         string                 `json:"ghe_domain"`
	ResourceType      string                 `json:"resource_type"`
	RunnerUser        string                 `json:"runner_user"`
	RunnerVersion     string                 `json:"runner_version"`
	ProviderURL       string                 `json:"provider_url"`
	Status            datastore.TargetStatus `json:"status"`
	StatusDescription string                 `json:"status_description"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

func sortUserTarget(uts []UserTarget) []UserTarget {
	sort.SliceStable(uts, func(i, j int) bool {
		if uts[i].CreatedAt != uts[j].CreatedAt {
			return uts[i].CreatedAt.After(uts[j].CreatedAt)
		}

		iType := datastore.UnmarshalResourceTypeString(uts[i].ResourceType)
		jType := datastore.UnmarshalResourceTypeString(uts[j].ResourceType)

		return iType < jType
	})

	return uts
}

// function pointer (for testing)
var (
	GHExistGitHubRepositoryFunc = gh.ExistGitHubRepository
	GHExistRunnerReleases       = gh.ExistRunnerReleases
	GHListRunnersFunc           = gh.ListRunners
	GHIsInstalledGitHubApp      = gh.IsInstalledGitHubApp
	GHGenerateGitHubAppsToken   = gh.GenerateGitHubAppsToken
	GHNewClientApps             = gh.NewClientGitHubApps
)

func handleTargetList(w http.ResponseWriter, r *http.Request, ds datastore.Datastore) {
	ctx := r.Context()

	ts, err := datastore.ListTargets(ctx, ds)
	if err != nil {
		logger.Logf(false, "failed to retrieve list of target: %+v", err)
		outputErrorMsg(w, http.StatusInternalServerError, "datastore read error")
	}

	var targets []UserTarget
	for _, t := range ts {
		ut := sanitizeTarget(t)
		targets = append(targets, ut)
	}

	targets = sortUserTarget(targets)

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(targets)
}

func handleTargetRead(w http.ResponseWriter, r *http.Request, ds datastore.Datastore) {
	ctx := r.Context()
	targetID, err := parseReqTargetID(r)
	if err != nil {
		logger.Logf(false, "failed to decode request body: %+v", err)
		outputErrorMsg(w, http.StatusBadRequest, "incorrect target id")
		return
	}

	target, err := ds.GetTarget(ctx, targetID)
	if err != nil {
		logger.Logf(false, "failed to retrieve target from datastore: %+v", err)
		outputErrorMsg(w, http.StatusInternalServerError, "datastore read error")
		return
	}

	ut := sanitizeTarget(*target)

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(ut)
}

func sanitizeTarget(t datastore.Target) UserTarget {
	ut := UserTarget{
		UUID:              t.UUID,
		Scope:             t.Scope,
		TokenExpiredAt:    t.TokenExpiredAt,
		GHEDomain:         t.GHEDomain.String,
		ResourceType:      t.ResourceType.String(),
		RunnerUser:        t.RunnerUser.String,
		RunnerVersion:     t.RunnerVersion.String,
		ProviderURL:       t.ProviderURL.String,
		Status:            t.Status,
		StatusDescription: t.StatusDescription.String,
		CreatedAt:         t.CreatedAt,
		UpdatedAt:         t.UpdatedAt,
	}

	return ut
}

func handleTargetUpdate(w http.ResponseWriter, r *http.Request, ds datastore.Datastore) {
	ctx := r.Context()
	targetID, err := parseReqTargetID(r)
	if err != nil {
		logger.Logf(false, "failed to decode request body: %+v", err)
		outputErrorMsg(w, http.StatusBadRequest, "incorrect target id")
		return
	}

	inputTarget := TargetCreateParam{}
	if err := json.NewDecoder(r.Body).Decode(&inputTarget); err != nil {
		logger.Logf(false, "failed to decode request body: %+v", err)
		outputErrorMsg(w, http.StatusBadRequest, "json decode error")
		return
	}
	newTarget := inputTarget.ToDS("", time.Time{})

	oldTarget, err := ds.GetTarget(ctx, targetID)
	if err != nil {
		logger.Logf(false, "failed to get target: %+v", err)
		outputErrorMsg(w, http.StatusBadRequest, "incorrect target id (not found)")
		return
	}
	if err := validateUpdateTarget(*oldTarget, newTarget); err != nil {
		logger.Logf(false, "input error in validateUpdateTarget: %+v", err)
		outputErrorMsg(w, http.StatusBadRequest, err.Error())
		return
	}

	resourceType, runnerVersion, runnerUser, providerURL := getWillUpdateTargetVariable(getWillUpdateTargetVariableOld{
		resourceType:  oldTarget.ResourceType,
		runnerVersion: oldTarget.RunnerVersion,
		runnerUser:    oldTarget.RunnerUser,
		providerURL:   oldTarget.ProviderURL,
	}, getWillUpdateTargetVariableNew{
		resourceType:  inputTarget.ResourceType,
		runnerVersion: inputTarget.RunnerVersion,
		runnerUser:    inputTarget.RunnerUser,
		providerURL:   inputTarget.ProviderURL,
	})
	if err := ds.UpdateTargetParam(ctx, targetID, resourceType, runnerVersion, runnerUser, providerURL); err != nil {
		logger.Logf(false, "failed to ds.UpdateTargetParam: %+v", err)
		outputErrorMsg(w, http.StatusInternalServerError, "datastore update error")
		return
	}

	updatedTarget, err := ds.GetTarget(ctx, targetID)
	if err != nil {
		logger.Logf(false, "failed to get recently target in datastore: %+v", err)
		outputErrorMsg(w, http.StatusInternalServerError, "datastore get error")
		return
	}
	ut := sanitizeTarget(*updatedTarget)

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(ut)
}

func handleTargetDelete(w http.ResponseWriter, r *http.Request, ds datastore.Datastore) {
	ctx := r.Context()
	targetID, err := parseReqTargetID(r)
	if err != nil {
		logger.Logf(false, "failed to decode request body: %+v", err)
		outputErrorMsg(w, http.StatusBadRequest, "incorrect target id")
		return
	}

	target, err := ds.GetTarget(ctx, targetID)
	if err != nil {
		logger.Logf(false, "failed to get target: %+v", err)
		outputErrorMsg(w, http.StatusBadRequest, "incorrect target id (not found)")
		return
	}
	switch target.Status {
	case datastore.TargetStatusRunning:
		logger.Logf(true, "%s is running now", targetID)
		outputErrorMsg(w, http.StatusBadRequest, "target has running runner now, please stop all runner")
		return
	case datastore.TargetStatusDeleted:
		outputErrorMsg(w, http.StatusBadRequest, "target is already deleted")
		return
	}

	if err := ds.DeleteTarget(ctx, targetID); err != nil {
		logger.Logf(false, "failed to delete target in datastore: %+v", err)
		outputErrorMsg(w, http.StatusInternalServerError, "datastore delete error")
		return
	}

	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.WriteHeader(http.StatusNoContent)
}

func parseReqTargetID(r *http.Request) (uuid.UUID, error) {
	targetIDStr := pat.Param(r, "id")
	targetID, err := uuid.FromString(targetIDStr)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("failed to parse target id: %w", err)
	}

	return targetID, nil
}

// ErrorResponse is error response
type ErrorResponse struct {
	Error string `json:"error"`
}

func outputErrorMsg(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json;charset=utf-8")

	w.WriteHeader(status)

	json.NewEncoder(w).Encode(ErrorResponse{Error: msg})
}

// validateUpdateTarget check input target that can valid input in update.
func validateUpdateTarget(old, new datastore.Target) error {
	oldv := old
	newv := new

	if new.RunnerVersion.Valid {
		if err := validRunnerVersion(new.RunnerVersion.String); err != nil {
			logger.Logf(false, "invalid input runner_version (runner_version: %s): %+v", new.RunnerVersion.String, err)
			return fmt.Errorf("invalid input runner_version (runner_version: %s): %w", new.RunnerVersion.String, err)
		}
	}

	for _, t := range []*datastore.Target{&oldv, &newv} {
		t.UUID = uuid.UUID{}

		// can update variables
		t.ResourceType = datastore.ResourceTypeUnknown
		t.RunnerVersion = sql.NullString{}
		t.RunnerUser = sql.NullString{}
		t.ProviderURL = sql.NullString{}

		// time
		t.TokenExpiredAt = time.Time{}
		t.CreatedAt = time.Time{}
		t.UpdatedAt = time.Time{}

		// generated
		t.Status = ""
		t.StatusDescription = sql.NullString{}
		t.GitHubToken = ""
	}

	changelog, err := diff.Diff(oldv, newv)
	if err != nil {
		logger.Logf(false, "failed to check diff: %+v", err)
		return fmt.Errorf("failed to check diff: %w", err)
	}
	if len(changelog) != 0 {
		logger.Logf(false, "invalid updatable parameter: %+v", changelog)

		var invalidFields []string
		for _, cl := range changelog {
			if len(cl.Path) == 2 && !strings.EqualFold(cl.Path[1], "String") {
				continue
			}

			fieldName := cl.Path[0]
			invalidFields = append(invalidFields, fieldName)
		}

		return fmt.Errorf("invalid input: can't updatable fields (%s)", strings.Join(invalidFields, ", "))
	}

	return nil
}

func isValidTargetCreateParam(input TargetCreateParam) error {
	if input.Scope == "" || input.ResourceType == datastore.ResourceTypeUnknown {
		return fmt.Errorf("scope, resource_type must be set")
	}

	if input.GHEDomain != "" {
		if strings.EqualFold(input.GHEDomain, "https://github.com") {
			return fmt.Errorf("ghe_domain must not https://github.com, please set blank")
		}

		if _, err := url.Parse(input.GHEDomain); err != nil {
			return fmt.Errorf("domain of GitHub Enterprise is not valid URL: %w", err)
		}
	}

	if input.RunnerVersion != nil {
		if err := validRunnerVersion(*input.RunnerVersion); err != nil {
			logger.Logf(false, "invalid input runner_version (runner_version: %s): %+v", *input.RunnerVersion, err)
			return fmt.Errorf("invalid input runner_version (runner_version: %s): %w", *input.RunnerVersion, err)
		}
	}

	return nil
}

func validRunnerVersion(runnerVersion string) error {
	if !strings.HasPrefix(runnerVersion, "v") {
		return fmt.Errorf("runner_version must has prefix 'v'")
	}

	s := strings.Split(runnerVersion, ".")
	if len(s) != 3 {
		return fmt.Errorf("runner_version must has version of major, sem, patch")
	}

	if err := GHExistRunnerReleases(runnerVersion); err != nil {
		return fmt.Errorf("runner_version is not found in GitHub Release: %w", err)
	}

	return nil
}

func toNullString(input *string) sql.NullString {
	if input == nil || strings.EqualFold(*input, "") {
		return sql.NullString{
			Valid: false,
		}
	}

	return sql.NullString{
		Valid:  true,
		String: *input,
	}
}

// ToDS convert to datastore.Target
func (t *TargetCreateParam) ToDS(appToken string, tokenExpired time.Time) datastore.Target {
	gheDomain := toNullString(&t.GHEDomain)
	runnerUser := toNullString(t.RunnerUser)
	runnerVersion := toNullString(t.RunnerVersion)
	providerURL := toNullString(t.ProviderURL)

	return datastore.Target{
		UUID:           t.UUID,
		Scope:          t.Scope,
		GitHubToken:    appToken,
		TokenExpiredAt: tokenExpired,
		GHEDomain:      gheDomain,
		ResourceType:   t.ResourceType,
		RunnerUser:     runnerUser,
		RunnerVersion:  runnerVersion,
		ProviderURL:    providerURL,
	}
}

type getWillUpdateTargetVariableOld struct {
	resourceType  datastore.ResourceType
	runnerVersion sql.NullString
	runnerUser    sql.NullString
	providerURL   sql.NullString
}

type getWillUpdateTargetVariableNew struct {
	resourceType  datastore.ResourceType
	runnerVersion *string
	runnerUser    *string
	providerURL   *string
}

func getWillUpdateTargetVariable(oldParam getWillUpdateTargetVariableOld, newParam getWillUpdateTargetVariableNew) (datastore.ResourceType, sql.NullString, sql.NullString, sql.NullString) {
	rt := oldParam.resourceType
	if newParam.resourceType != datastore.ResourceTypeUnknown {
		rt = newParam.resourceType
	}

	runnerVersion := getWillUpdateTargetVariableString(oldParam.runnerVersion, newParam.runnerVersion)
	runnerUser := getWillUpdateTargetVariableString(oldParam.runnerUser, newParam.runnerUser)
	providerURL := getWillUpdateTargetVariableString(oldParam.providerURL, newParam.providerURL)

	return rt, runnerVersion, runnerUser, providerURL
}

func getWillUpdateTargetVariableString(old sql.NullString, new *string) sql.NullString {
	if new == nil {
		return old
	}
	return toNullString(new)
}
