package v1

import (
	"context"

	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/bytebase/bytebase/backend/common"
	"github.com/bytebase/bytebase/backend/store"
	storepb "github.com/bytebase/bytebase/proto/generated-go/store"
	v1pb "github.com/bytebase/bytebase/proto/generated-go/v1"
)

type ReleaseService struct {
	v1pb.UnimplementedReleaseServiceServer
	store *store.Store
}

func NewReleaseService(store *store.Store) *ReleaseService {
	return &ReleaseService{
		store: store,
	}
}

func (s *ReleaseService) CreateRelease(ctx context.Context, request *v1pb.CreateReleaseRequest) (*v1pb.Release, error) {
	if request.Release == nil {
		return nil, status.Errorf(codes.InvalidArgument, "request.Release cannot be nil")
	}

	user, ok := ctx.Value(common.UserContextKey).(*store.UserMessage)
	if !ok {
		return nil, status.Errorf(codes.Internal, "user not found")
	}

	projectID, err := common.GetProjectID(request.Parent)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to get project id, err: %v", err)
	}
	project, err := s.store.GetProjectV2(ctx, &store.FindProjectMessage{ResourceID: &projectID})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to find project, err: %v", err)
	}
	if project == nil {
		return nil, status.Errorf(codes.NotFound, "project %v not found", projectID)
	}

	files, err := convertReleaseFiles(ctx, s.store, request.Release.Files)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert files, err: %v", err)
	}

	releaseMessage := &store.ReleaseMessage{
		ProjectUID: project.UID,
		Payload: &storepb.ReleasePayload{
			Title:     request.Release.Title,
			Files:     files,
			VcsSource: convertReleaseVcsSource(request.Release.VcsSource),
		},
	}

	release, err := s.store.CreateRelease(ctx, releaseMessage, user.ID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create release, err: %v", err)
	}

	converted, err := convertToRelease(ctx, s.store, release)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert release, err: %v", err)
	}

	return converted, nil
}

func (s *ReleaseService) GetRelease(ctx context.Context, request *v1pb.GetReleaseRequest) (*v1pb.Release, error) {
	releaseUID, err := common.GetReleaseUID(request.Name)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to get release uid, err: %v", err)
	}
	releaseMessage, err := s.store.GetRelease(ctx, releaseUID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get release, err: %v", err)
	}
	if releaseMessage == nil {
		return nil, status.Errorf(codes.NotFound, "release %v not found", releaseUID)
	}
	release, err := convertToRelease(ctx, s.store, releaseMessage)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert to release, err: %v", err)
	}
	return release, nil
}

func (s *ReleaseService) ListReleases(ctx context.Context, request *v1pb.ListReleasesRequest) (*v1pb.ListReleasesResponse, error) {
	if request.PageSize < 0 {
		return nil, status.Errorf(codes.InvalidArgument, "page size must be non-negative: %d", request.PageSize)
	}

	projectID, err := common.GetProjectID(request.Parent)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to get project id, err: %v", err)
	}
	project, err := s.store.GetProjectV2(ctx, &store.FindProjectMessage{ResourceID: &projectID})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to find project, err: %v", err)
	}
	if project == nil {
		return nil, status.Errorf(codes.NotFound, "project %v not found", projectID)
	}

	limit, offset, err := parseLimitAndOffset(request.PageToken, int(request.PageSize))
	if err != nil {
		return nil, err
	}
	limitPlusOne := limit + 1

	releaseFind := &store.FindReleaseMessage{
		ProjectUID:  &project.UID,
		Limit:       &limitPlusOne,
		Offset:      &offset,
		ShowDeleted: request.ShowDeleted,
	}

	releaseMessages, err := s.store.ListReleases(ctx, releaseFind)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list releases, err: %v", err)
	}

	var nextPageToken string
	if len(releaseMessages) == limitPlusOne {
		pageToken, err := getPageToken(limit, offset+limit)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to get next page token, error: %v", err)
		}
		nextPageToken = pageToken
		releaseMessages = releaseMessages[:limit]
	}

	releases, err := convertToReleases(ctx, s.store, releaseMessages)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert to release, err: %v", err)
	}

	return &v1pb.ListReleasesResponse{
		Releases:      releases,
		NextPageToken: nextPageToken,
	}, nil
}

func (s *ReleaseService) UpdateRelease(ctx context.Context, request *v1pb.UpdateReleaseRequest) (*v1pb.Release, error) {
	if request.UpdateMask == nil {
		return nil, status.Errorf(codes.InvalidArgument, "update_mask must be set")
	}

	releaseUID, err := common.GetReleaseUID(request.Release.Name)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to get release uid, err: %v", err)
	}
	release, err := s.store.GetRelease(ctx, releaseUID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get release, err: %v", err)
	}
	if release == nil {
		return nil, status.Errorf(codes.NotFound, "release %v not found", releaseUID)
	}
	if release.Deleted {
		return nil, status.Errorf(codes.FailedPrecondition, "release %d is deleted", releaseUID)
	}

	update := &store.UpdateReleaseMessage{
		UID: releaseUID,
	}
	for _, path := range request.UpdateMask.Paths {
		if path == "title" {
			payload := release.Payload
			payload.Title = request.Release.Title
			update.Payload = payload
		}
	}

	releaseMessage, err := s.store.UpdateRelease(ctx, update)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update release, err: %v", err)
	}
	converted, err := convertToRelease(ctx, s.store, releaseMessage)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert release, err: %v", err)
	}
	return converted, nil
}

func (s *ReleaseService) DeleteRelease(ctx context.Context, request *v1pb.DeleteReleaseRequest) (*emptypb.Empty, error) {
	releaseUID, err := common.GetReleaseUID(request.Name)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to get release uid, err: %v", err)
	}
	if _, err := s.store.UpdateRelease(ctx, &store.UpdateReleaseMessage{
		UID:     releaseUID,
		Deleted: &deletePatch,
	}); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete release, err: %v", err)
	}
	return &emptypb.Empty{}, nil
}

func (s *ReleaseService) UndeleteRelease(ctx context.Context, request *v1pb.UndeleteReleaseRequest) (*v1pb.Release, error) {
	releaseUID, err := common.GetReleaseUID(request.Name)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to get release uid, err: %v", err)
	}
	releaseMessage, err := s.store.UpdateRelease(ctx, &store.UpdateReleaseMessage{
		UID:     releaseUID,
		Deleted: &undeletePatch,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to undelete release, err: %v", err)
	}
	release, err := convertToRelease(ctx, s.store, releaseMessage)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert release, err: %v", err)
	}
	return release, nil
}

func convertToReleases(ctx context.Context, s *store.Store, releases []*store.ReleaseMessage) ([]*v1pb.Release, error) {
	var rs []*v1pb.Release
	for _, release := range releases {
		r, err := convertToRelease(ctx, s, release)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to convert to release")
		}
		rs = append(rs, r)
	}
	return rs, nil
}

func convertToRelease(ctx context.Context, s *store.Store, release *store.ReleaseMessage) (*v1pb.Release, error) {
	r := &v1pb.Release{
		Title:      release.Payload.Title,
		CreateTime: timestamppb.New(release.CreatedTime),
		VcsSource:  convertToReleaseVcsSource(release.Payload.VcsSource),
		State:      convertDeletedToState(release.Deleted),
	}

	files, err := convertToReleaseFiles(ctx, s, release.Payload.Files)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to convert release files")
	}
	r.Files = files

	project, err := s.GetProjectV2(ctx, &store.FindProjectMessage{UID: &release.ProjectUID})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to find project")
	}
	if project == nil {
		return nil, errors.Wrapf(err, "project %v not found", release.ProjectUID)
	}
	r.Name = common.FormatReleaseName(project.ResourceID, release.UID)

	creator, err := s.GetUserByID(ctx, release.CreatorUID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get release creator")
	}
	r.Creator = common.FormatUserEmail(creator.Email)

	return r, nil
}

func convertToReleaseFiles(ctx context.Context, s *store.Store, files []*storepb.ReleasePayload_File) ([]*v1pb.Release_File, error) {
	if files == nil {
		return nil, nil
	}
	var v1Files []*v1pb.Release_File
	for _, f := range files {
		_, sheetUID, err := common.GetProjectResourceIDSheetUID(f.Sheet)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get sheetUID from %q", f.Sheet)
		}
		sheet, err := s.GetSheet(ctx, &store.FindSheetMessage{UID: &sheetUID})
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get sheet %q", f.Sheet)
		}
		if sheet == nil {
			return nil, errors.Errorf("sheet %q not found", f.Sheet)
		}
		v1Files = append(v1Files, &v1pb.Release_File{
			Name:          f.Name,
			Sheet:         f.Sheet,
			SheetSha256:   f.SheetSha256,
			Type:          v1pb.ReleaseFileType(f.Type),
			Version:       f.Version,
			Statement:     sheet.Statement,
			StatementSize: sheet.Size,
		})
	}
	return v1Files, nil
}

func convertToReleaseVcsSource(vs *storepb.ReleasePayload_VCSSource) *v1pb.Release_VCSSource {
	if vs == nil {
		return nil
	}
	return &v1pb.Release_VCSSource{
		VcsType:        v1pb.VCSType(vs.VcsType),
		PullRequestUrl: vs.PullRequestUrl,
	}
}

func convertReleaseFiles(ctx context.Context, s *store.Store, files []*v1pb.Release_File) ([]*storepb.ReleasePayload_File, error) {
	if files == nil {
		return nil, nil
	}
	var rFiles []*storepb.ReleasePayload_File
	for _, f := range files {
		_, sheetUID, err := common.GetProjectResourceIDSheetUID(f.Sheet)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get sheetUID from %q", f.Sheet)
		}
		sheet, err := s.GetSheet(ctx, &store.FindSheetMessage{
			UID:      &sheetUID,
			LoadFull: false,
		})
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get sheet %q", f.Sheet)
		}
		if sheet == nil {
			return nil, errors.Errorf("sheet %q not found", f.Sheet)
		}

		rFiles = append(rFiles, &storepb.ReleasePayload_File{
			Name:        f.Name,
			Sheet:       f.Sheet,
			SheetSha256: sheet.Sha256,
			Type:        storepb.ReleaseFileType(f.Type),
			Version:     f.Version,
		})
	}
	return rFiles, nil
}

func convertReleaseVcsSource(vs *v1pb.Release_VCSSource) *storepb.ReleasePayload_VCSSource {
	if vs == nil {
		return nil
	}
	return &storepb.ReleasePayload_VCSSource{
		VcsType:        storepb.VCSType(vs.VcsType),
		PullRequestUrl: vs.PullRequestUrl,
	}
}
