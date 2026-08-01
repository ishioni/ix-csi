package driver

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/truenas/truenas-csi/pkg/client"
)

const (
	detachedSnapshotSourcePrefix  = "csi-detached-snapshot-"
	detachedVolumeSourcePrefix    = "csi-detached-volume-"
	detachedSnapshotProperty      = "truenas-csi:detached_snapshot"
	detachedSnapshotPropertyValue = "true"
)

type detachedSnapshotInfo struct {
	ID             string
	SourceVolumeID string
	Name           string
	Dataset        client.Dataset
}

func detachedBoolParameter(parameters map[string]string, key string) (bool, error) {
	value, ok := parameters[key]
	if !ok || value == "" {
		return false, nil
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if value != "true" && value != "false" {
		return false, fmt.Errorf("invalid %s value: %s (valid: true, false)", key, value)
	}
	return value == "true", nil
}

func detachedParameterEnabled(parameters map[string]string, key string) bool {
	enabled, _ := detachedBoolParameter(parameters, key)
	return enabled
}

func datasetPathsOverlap(first, second string) bool {
	first = strings.TrimSuffix(strings.TrimSpace(first), "/")
	second = strings.TrimSuffix(strings.TrimSpace(second), "/")
	return first != "" && second != "" &&
		(first == second || strings.HasPrefix(first, second+"/") || strings.HasPrefix(second, first+"/"))
}

func validateDatasetPath(path string) error {
	if path == "" || strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return fmt.Errorf("dataset path must be non-empty and relative")
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.Contains(part, "@") {
			return fmt.Errorf("invalid dataset path %q", path)
		}
	}
	return nil
}

// detachedSnapshotParts parses the CSI ID for an independent snapshot. The
// source volume ID is everything before the final slash because volume IDs can
// themselves contain nested dataset paths.
func detachedSnapshotParts(id string) (sourceVolumeID, snapshotName string, err error) {
	if strings.Contains(id, "@") {
		return "", "", fmt.Errorf("snapshot ID %q is a regular ZFS snapshot", id)
	}
	if err := validateDatasetPath(id); err != nil {
		return "", "", err
	}
	separator := strings.LastIndexByte(id, '/')
	if separator <= 0 || separator == len(id)-1 {
		return "", "", fmt.Errorf("invalid detached snapshot ID %q", id)
	}
	sourceVolumeID = id[:separator]
	snapshotName = id[separator+1:]
	if _, _, err := splitVolumeID(sourceVolumeID); err != nil {
		return "", "", fmt.Errorf("invalid source volume in detached snapshot ID %q: %w", id, err)
	}
	return sourceVolumeID, snapshotName, nil
}

func splitVolumeID(volumeID string) (string, string, error) {
	parts := strings.SplitN(volumeID, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid volume ID %q", volumeID)
	}
	if err := validateDatasetPath(volumeID); err != nil {
		return "", "", err
	}
	return parts[0], parts[1], nil
}

func detachedSnapshotDataset(parent, id string) (string, error) {
	if err := validateDatasetPath(parent); err != nil {
		return "", err
	}
	if _, _, err := detachedSnapshotParts(id); err != nil {
		return "", err
	}
	return strings.TrimSuffix(parent, "/") + "/" + id, nil
}

func datasetCapacity(dataset *client.Dataset) int64 {
	if dataset == nil {
		return 0
	}
	if dataset.Type == datasetTypeVolume && dataset.Volsize > 0 {
		return dataset.Volsize
	}
	if dataset.RefQuota > 0 {
		return dataset.RefQuota
	}
	return dataset.Used
}

func (s *ControllerServer) detachedParent() (string, error) {
	parent := s.driver.DetachedSnapshotParentDataset()
	if parent == "" {
		return "", fmt.Errorf("detached snapshots require TRUENAS_DETACHED_SNAPSHOT_PARENT_DATASET")
	}
	if err := validateDatasetPath(parent); err != nil {
		return "", fmt.Errorf("invalid detached snapshot dataset parent %q: %w", parent, err)
	}
	return parent, nil
}

func (s *ControllerServer) validateDetachedSource(sourceDataset string) error {
	parent, err := s.detachedParent()
	if err != nil {
		return err
	}
	if err := validateDatasetPath(sourceDataset); err != nil {
		return err
	}
	if datasetPathsOverlap(parent, sourceDataset) {
		return fmt.Errorf("detached snapshot dataset parent %q overlaps source dataset %q", parent, sourceDataset)
	}
	return nil
}

func (s *ControllerServer) validateDetachedTarget(targetDataset string) error {
	parent, err := s.detachedParent()
	if err != nil {
		return err
	}
	if err := validateDatasetPath(targetDataset); err != nil {
		return err
	}
	if datasetPathsOverlap(parent, targetDataset) {
		return fmt.Errorf("detached snapshot dataset parent %q overlaps target dataset %q", parent, targetDataset)
	}
	return nil
}

func (s *ControllerServer) ensureDataset(ctx context.Context, datasetPath string) error {
	if _, err := s.driver.Client().GetDataset(ctx, datasetPath); err == nil {
		return nil
	} else if !client.IsNotFoundError(err) {
		return err
	}

	_, err := s.driver.Client().CreateDataset(ctx, &client.DatasetCreateOptions{
		Name:            datasetPath,
		Type:            datasetTypeFilesystem,
		CreateAncestors: true,
	})
	if err != nil {
		if _, getErr := s.driver.Client().GetDataset(ctx, datasetPath); getErr == nil {
			return nil
		}
		return err
	}
	return nil
}

func (s *ControllerServer) ensureTargetParent(ctx context.Context, targetDataset string) error {
	separator := strings.LastIndexByte(targetDataset, '/')
	if separator <= 0 {
		return nil
	}
	return s.ensureDataset(ctx, targetDataset[:separator])
}

func (s *ControllerServer) createTemporarySnapshot(ctx context.Context, dataset, name string) (string, error) {
	snapshotID := dataset + "@" + name
	if _, err := s.driver.Client().CreateSnapshot(ctx, dataset, name, false); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return "", err
		}
	}
	return snapshotID, nil
}

func (s *ControllerServer) deleteSnapshotIfPresent(ctx context.Context, snapshotID string) error {
	if err := s.driver.Client().DeleteSnapshot(ctx, snapshotID); err != nil && !client.IsNotFoundError(err) {
		return err
	}
	return nil
}

// transferDetachedSnapshot sends one snapshot to an independent dataset and
// removes the received snapshot afterwards. deleteSource is used only for the
// temporary source snapshots created for volume clones and detached snapshots.
func (s *ControllerServer) transferDetachedSnapshot(ctx context.Context, sourceSnapshotID, targetDataset string, deleteSource bool) error {
	separator := strings.LastIndexByte(sourceSnapshotID, '@')
	if separator <= 0 || separator == len(sourceSnapshotID)-1 {
		return fmt.Errorf("invalid source snapshot ID %q", sourceSnapshotID)
	}
	sourceDataset := sourceSnapshotID[:separator]
	snapshotName := sourceSnapshotID[separator+1:]
	if err := validateDatasetPath(targetDataset); err != nil {
		return err
	}
	if err := s.ensureTargetParent(ctx, targetDataset); err != nil {
		return fmt.Errorf("failed to create target dataset parent: %w", err)
	}

	_, targetErr := s.driver.Client().GetDataset(ctx, targetDataset)
	transferErr := error(nil)
	if client.IsNotFoundError(targetErr) {
		jobID, err := s.driver.Client().RunReplicationOnetime(ctx, &client.ReplicationRunOnetimeOptions{
			Direction:       "PUSH",
			Transport:       "LOCAL",
			SourceDatasets:  []string{sourceDataset},
			TargetDataset:   targetDataset,
			NameRegex:       "^" + regexp.QuoteMeta(snapshotName) + "$",
			Recursive:       false,
			Properties:      false,
			Readonly:        "IGNORE",
			RetentionPolicy: "NONE",
			OnlyFromScratch: true,
		})
		if err != nil {
			transferErr = err
		} else {
			job, waitErr := s.driver.Client().WaitForJob(ctx, jobID, time.Second)
			if waitErr != nil {
				transferErr = waitErr
			} else if strings.ToUpper(job.State) != "SUCCESS" {
				transferErr = fmt.Errorf("TrueNAS replication job %d finished in state %s: %s %s", job.ID, job.State, job.Error, job.Exception)
			}
		}
	} else if targetErr != nil {
		transferErr = targetErr
	}

	// The received dataset must not retain a snapshot that makes it depend on
	// the source. Clean this up even if the job reported failure: TrueNAS may
	// have created the target before returning the failure.
	targetSnapshotID := targetDataset + "@" + snapshotName
	if err := s.deleteSnapshotIfPresent(ctx, targetSnapshotID); err != nil && transferErr == nil {
		transferErr = fmt.Errorf("failed to remove received snapshot %s: %w", targetSnapshotID, err)
	}
	if deleteSource {
		if err := s.deleteSnapshotIfPresent(ctx, sourceSnapshotID); err != nil && transferErr == nil {
			transferErr = fmt.Errorf("failed to remove temporary source snapshot %s: %w", sourceSnapshotID, err)
		}
	}
	return transferErr
}

func (s *ControllerServer) cloneDetachedSnapshot(ctx context.Context, snapshotID, targetDataset string) error {
	parent, err := s.detachedParent()
	if err != nil {
		return err
	}
	if strings.Contains(snapshotID, "@") {
		separator := strings.LastIndexByte(snapshotID, '@')
		if separator <= 0 {
			return fmt.Errorf("invalid source snapshot ID %q", snapshotID)
		}
		if err := s.validateDetachedSource(snapshotID[:separator]); err != nil {
			return err
		}
		return s.transferDetachedSnapshot(ctx, snapshotID, targetDataset, false)
	}

	sourceVolumeID, _, err := detachedSnapshotParts(snapshotID)
	if err != nil {
		return err
	}
	if err := s.validateDetachedTarget(targetDataset); err != nil {
		return err
	}
	sourceDataset, err := detachedSnapshotDataset(parent, snapshotID)
	if err != nil {
		return err
	}
	dataset, err := s.driver.Client().GetDataset(ctx, sourceDataset)
	if err != nil {
		if client.IsNotFoundError(err) {
			return fmt.Errorf("detached source snapshot %s was not found", snapshotID)
		}
		return err
	}
	if !isDetachedSnapshotDataset(dataset) {
		return fmt.Errorf("detached source snapshot %s is not managed by truenas-csi", snapshotID)
	}
	tempName := detachedVolumeSourcePrefix + SanitizeVolumeName(strings.ReplaceAll(sourceVolumeID, "/", "-"))
	tempSnapshotID, err := s.createTemporarySnapshot(ctx, sourceDataset, tempName)
	if err != nil {
		return fmt.Errorf("failed to create temporary snapshot for detached clone: %w", err)
	}
	return s.transferDetachedSnapshot(ctx, tempSnapshotID, targetDataset, true)
}

func (s *ControllerServer) createDetachedSnapshot(ctx context.Context, sourceVolumeID, snapshotName, sourceDataset string) (string, error) {
	parent, err := s.detachedParent()
	if err != nil {
		return "", err
	}
	if err := s.validateDetachedSource(sourceDataset); err != nil {
		return "", err
	}
	detachedID := sourceVolumeID + "/" + snapshotName
	targetDataset, err := detachedSnapshotDataset(parent, detachedID)
	if err != nil {
		return "", err
	}

	if existing, err := s.findDetachedSnapshotByName(ctx, snapshotName); err != nil {
		return "", err
	} else if existing != nil {
		if existing.ID == detachedID {
			return detachedID, nil
		}
		return "", fmt.Errorf("detached snapshot name %s already exists for source volume %s", snapshotName, existing.SourceVolumeID)
	}

	if existing, err := s.driver.Client().GetDataset(ctx, targetDataset); err == nil && existing != nil {
		if !isDetachedSnapshotDataset(existing) {
			return "", fmt.Errorf("detached snapshot target %s already exists and is not managed by truenas-csi", targetDataset)
		}
		return detachedID, nil
	} else if err != nil && !client.IsNotFoundError(err) {
		return "", err
	}

	if err := s.ensureDataset(ctx, parent); err != nil {
		return "", fmt.Errorf("failed to create detached snapshot parent: %w", err)
	}
	if err := s.ensureDataset(ctx, strings.TrimSuffix(parent, "/")+"/"+sourceVolumeID); err != nil {
		return "", fmt.Errorf("failed to create detached snapshot container: %w", err)
	}

	tempName := detachedSnapshotSourcePrefix + snapshotName
	tempSnapshotID, err := s.createTemporarySnapshot(ctx, sourceDataset, tempName)
	if err != nil {
		return "", fmt.Errorf("failed to create temporary snapshot: %w", err)
	}
	if err := s.transferDetachedSnapshot(ctx, tempSnapshotID, targetDataset, true); err != nil {
		return "", err
	}
	if err := s.markDetachedSnapshot(ctx, targetDataset); err != nil {
		return "", fmt.Errorf("failed to mark detached snapshot: %w", err)
	}
	return detachedID, nil
}

func (s *ControllerServer) cloneDetachedVolume(ctx context.Context, sourceDataset, targetDataset, volumeID string) error {
	if err := s.validateDetachedSource(sourceDataset); err != nil {
		return err
	}
	if err := s.validateDetachedTarget(targetDataset); err != nil {
		return err
	}
	tempName := detachedVolumeSourcePrefix + SanitizeVolumeName(strings.ReplaceAll(volumeID, "/", "-"))
	tempSnapshotID, err := s.createTemporarySnapshot(ctx, sourceDataset, tempName)
	if err != nil {
		return fmt.Errorf("failed to create temporary snapshot for detached volume clone: %w", err)
	}
	return s.transferDetachedSnapshot(ctx, tempSnapshotID, targetDataset, true)
}

func (s *ControllerServer) markDetachedSnapshot(ctx context.Context, datasetPath string) error {
	return s.driver.Client().UpdateDataset(ctx, datasetPath, &client.DatasetUpdateOptions{
		UserProperties: []map[string]string{{
			"key":   detachedSnapshotProperty,
			"value": detachedSnapshotPropertyValue,
		}},
	})
}

func isDetachedSnapshotDataset(dataset *client.Dataset) bool {
	return dataset != nil && dataset.UserProperties[detachedSnapshotProperty] == detachedSnapshotPropertyValue
}

func datasetPathForEntry(dataset client.Dataset) string {
	if dataset.ID != "" {
		return dataset.ID
	}
	return dataset.Name
}

func (s *ControllerServer) listDetachedSnapshots(ctx context.Context, sourceVolumeID string) ([]detachedSnapshotInfo, error) {
	parent, err := s.detachedParent()
	if err != nil {
		return nil, err
	}
	datasets, err := s.driver.Client().ListDatasets(ctx, client.ExtractPoolFromPath(parent))
	if err != nil {
		return nil, err
	}
	prefix := parent + "/"
	if sourceVolumeID != "" {
		prefix += sourceVolumeID + "/"
	}

	result := make([]detachedSnapshotInfo, 0)
	for _, dataset := range datasets {
		if !isDetachedSnapshotDataset(&dataset) {
			continue
		}
		fullPath := datasetPathForEntry(dataset)
		if !strings.HasPrefix(fullPath, prefix) {
			continue
		}
		relative := strings.TrimPrefix(fullPath, parent+"/")
		separator := strings.LastIndexByte(relative, '/')
		if separator <= 0 || separator == len(relative)-1 {
			continue
		}
		sourceID := relative[:separator]
		if sourceVolumeID != "" && sourceID != sourceVolumeID {
			continue
		}
		name := relative[separator+1:]
		result = append(result, detachedSnapshotInfo{
			ID:             sourceID + "/" + name,
			SourceVolumeID: sourceID,
			Name:           name,
			Dataset:        dataset,
		})
	}
	return result, nil
}

func (s *ControllerServer) findDetachedSnapshotByName(ctx context.Context, name string) (*detachedSnapshotInfo, error) {
	snapshots, err := s.listDetachedSnapshots(ctx, "")
	if err != nil {
		return nil, err
	}
	for i := range snapshots {
		if snapshots[i].Name == name {
			return &snapshots[i], nil
		}
	}
	return nil, nil
}
