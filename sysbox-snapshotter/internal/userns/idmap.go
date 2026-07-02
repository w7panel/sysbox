package userns

import (
	"errors"
	"fmt"
	"strings"

	"github.com/opencontainers/runtime-spec/specs-go"
)

const invalidID = 1<<32 - 1

var invalidUser = User{Uid: invalidID, Gid: invalidID}

type User struct {
	Uid uint32
	Gid uint32
}

type IDMap struct {
	UidMap []specs.LinuxIDMapping `json:"UidMap"`
	GidMap []specs.LinuxIDMapping `json:"GidMap"`
}

func (i *IDMap) RootPair() (User, error) {
	uid, err := toHost(0, i.UidMap)
	if err != nil {
		return invalidUser, err
	}
	gid, err := toHost(0, i.GidMap)
	if err != nil {
		return invalidUser, err
	}
	return User{Uid: uid, Gid: gid}, nil
}

func (i *IDMap) Marshal() (string, string) {
	marshal := func(mappings []specs.LinuxIDMapping) string {
		var values []string
		for _, mapping := range mappings {
			values = append(values, serializeLinuxIDMapping(mapping))
		}
		return strings.Join(values, ",")
	}
	return marshal(i.UidMap), marshal(i.GidMap)
}

func (i *IDMap) Unmarshal(uidMap, gidMap string) error {
	unmarshal := func(raw string, appendFunc func(mapping specs.LinuxIDMapping)) error {
		if raw == "" {
			return nil
		}
		for _, entry := range strings.Split(raw, ",") {
			mapping, err := deserializeLinuxIDMapping(entry)
			if err != nil {
				return err
			}
			appendFunc(mapping)
		}
		return nil
	}
	if err := unmarshal(uidMap, func(mapping specs.LinuxIDMapping) {
		i.UidMap = append(i.UidMap, mapping)
	}); err != nil {
		return err
	}
	return unmarshal(gidMap, func(mapping specs.LinuxIDMapping) {
		i.GidMap = append(i.GidMap, mapping)
	})
}

func toHost(containerID uint32, idMap []specs.LinuxIDMapping) (uint32, error) {
	if idMap == nil {
		return containerID, nil
	}
	for _, mapping := range idMap {
		high, err := safeSum(mapping.ContainerID, mapping.Size)
		if err != nil {
			break
		}
		if containerID >= mapping.ContainerID && containerID < high {
			hostID, err := safeSum(mapping.HostID, containerID-mapping.ContainerID)
			if err != nil || hostID == invalidID {
				break
			}
			return hostID, nil
		}
	}
	return invalidID, fmt.Errorf("container ID %d cannot be mapped to a host ID", containerID)
}

func safeSum(x, y uint32) (uint32, error) {
	z := x + y
	if z < x || z < y {
		return invalidID, errors.New("ID overflow")
	}
	return z, nil
}

func serializeLinuxIDMapping(mapping specs.LinuxIDMapping) string {
	return fmt.Sprintf("%d:%d:%d", mapping.ContainerID, mapping.HostID, mapping.Size)
}

func deserializeLinuxIDMapping(raw string) (specs.LinuxIDMapping, error) {
	var hostID, containerID, length int64
	if _, err := fmt.Sscanf(raw, "%d:%d:%d", &containerID, &hostID, &length); err != nil {
		return specs.LinuxIDMapping{}, fmt.Errorf("input value %s unparsable: %w", raw, err)
	}
	if containerID < 0 || containerID >= invalidID || hostID < 0 || hostID >= invalidID || length < 0 || length >= invalidID {
		return specs.LinuxIDMapping{}, fmt.Errorf("invalid mapping %q", raw)
	}
	return specs.LinuxIDMapping{ContainerID: uint32(containerID), HostID: uint32(hostID), Size: uint32(length)}, nil
}
