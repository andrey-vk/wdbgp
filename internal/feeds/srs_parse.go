package feeds

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
)

// parseDefaultRule loops items until srsItemFinal.
func parseDefaultRule(ctx context.Context, r io.Reader, cfg *ParseSRSConfig) ([]string, bool, error) {
	var allCIDRs []string
	var hasConstraint bool
	for {
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		default:
		}
		itemType, err := readByte(r)
		if err != nil {
			return nil, false, err
		}
		switch itemType {
		case srsItemIPCIDR:
			if !cfg.CIDRs {
				if err := skipIPSet(ctx, r); err != nil {
					return nil, false, err
				}
				continue
			}
			cidrs, err := readIPSetAsCIDRs(ctx, r)
			if err != nil {
				return nil, false, err
			}
			allCIDRs = append(allCIDRs, cidrs...)
		case srsItemSourceIPCIDR:
			if err := skipIPSet(ctx, r); err != nil {
				return nil, false, err
			}
			hasConstraint = true
		case srsItemDomain:
			// Domain matchers use a compact binary format. Domain is OR'd
			// with CIDRs, not AND'd — just skip the payload and continue.
			if err := skipDomainMatcher(r); err != nil {
				if err := skipRemainingItemsForConstraint(r); err != nil {
					return nil, false, err
				}
				return nil, false, nil
			}
		case srsItemDomainKeyword, srsItemDomainRegex:
			// DomainKeyword/DomainRegex are OR'd with CIDRs like srsItemDomain,
			// not AND'd — skip the payload without setting a constraint.
			if err := skipStringArray(ctx, r); err != nil {
				return nil, false, err
			}
		case srsItemAdGuardDomain:
			// Same as domain — compact binary format. Domain is OR'd
			// with CIDRs — just skip the payload and continue.
			if err := skipAdGuardMatcher(r); err != nil {
				if err := skipRemainingItemsForConstraint(r); err != nil {
					return nil, false, err
				}
				return nil, false, nil
			}
		case srsItemQueryType, srsItemSourcePort, srsItemPort:
			if err := skipUint16Array(r); err != nil {
				return nil, false, err
			}
			hasConstraint = true
		case srsItemNetwork,
			srsItemSourcePortRange, srsItemPortRange,
			srsItemProcessName, srsItemProcessPath, srsItemProcessPathRegex,
			srsItemPackageName, srsItemPackageNameRegex, srsItemWIFISSID, srsItemWIFIBSSID:
			if err := skipStringArray(ctx, r); err != nil {
				return nil, false, err
			}
			hasConstraint = true
		case srsItemNetworkType:
			if err := skipUint8Array(r); err != nil {
				return nil, false, err
			}
			hasConstraint = true
		case srsItemNetworkIsExpensive, srsItemNetworkIsConstrained:
			// no data, just the type byte
			hasConstraint = true
		case srsItemNetworkInterfaceAddress:
			if err := skipNetworkInterfaceAddress(ctx, r); err != nil {
				return nil, false, err
			}
			hasConstraint = true
		case srsItemDefaultInterfaceAddress:
			if err := skipPrefixArray(ctx, r); err != nil {
				return nil, false, err
			}
			hasConstraint = true
		case srsItemFinal:
			var invert uint8
			if err := binary.Read(r, binary.BigEndian, &invert); err != nil {
				return nil, false, err
			}
			if invert != 0 || hasConstraint {
				return nil, hasConstraint, nil // skip inverted or constrained rules
			}
			return allCIDRs, false, nil
		default:
			return nil, false, fmt.Errorf("unknown item type %d", itemType)
		}
	}
}

func parseLogicalRule(ctx context.Context, r io.Reader, cfg *ParseSRSConfig, depth int) ([]string, bool, error) {
	if depth > maxSRSRecursionDepth {
		return nil, false, fmt.Errorf("srs: logical rule recursion depth %d exceeds limit", depth)
	}
	mode, err := readByte(r)
	if err != nil {
		return nil, false, err
	}

	// Validate mode before reading sub-rules so we can skip them correctly.
	if mode != 0 && mode != 1 {
		br := &byteReader{r: r}
		subCount, err := binary.ReadUvarint(br)
		if err != nil {
			return nil, false, err
		}
		skipCfg := &ParseSRSConfig{}
		for i := uint64(0); i < subCount; i++ {
			rt, err := readByte(r)
			if err != nil {
				return nil, false, err
			}
			switch rt {
			case 0:
				if _, _, err := parseDefaultRule(ctx, r, skipCfg); err != nil {
					return nil, false, err
				}
			case 1:
				if _, _, err := parseLogicalRule(ctx, r, skipCfg, depth+1); err != nil {
					return nil, false, err
				}
			default:
				return nil, false, fmt.Errorf("srs: unknown sub-rule type %d", rt)
			}
		}
		var invert uint8
		if err := binary.Read(r, binary.BigEndian, &invert); err != nil {
			return nil, false, err
		}
		return nil, false, fmt.Errorf("srs: unknown logical rule mode %d", mode)
	}

	br := &byteReader{r: r}
	subCount, err := binary.ReadUvarint(br)
	if err != nil {
		return nil, false, err
	}

	// AND mode (0): compute intersection if all sub-rules are pure CIDR.
	if mode == 0 {
		var allGroups [][]string
		for i := uint64(0); i < subCount; i++ {
			rt, err := readByte(r)
			if err != nil {
				return nil, false, err
			}
			switch rt {
			case 0:
				cidrs, hasConstraint, err := parseDefaultRule(ctx, r, cfg)
				if err != nil {
					return nil, false, err
				}
				if hasConstraint || len(cidrs) == 0 {
					// Skip entire AND. Consume remaining sub-rules and invert byte.
					skipCfg := &ParseSRSConfig{}
					for j := i + 1; j < subCount; j++ {
						srt, skipErr := readByte(r)
						if skipErr != nil {
							return nil, false, skipErr
						}
						switch srt {
						case 0:
							if _, _, err := parseDefaultRule(ctx, r, skipCfg); err != nil {
								return nil, false, err
							}
						case 1:
							if _, _, err := parseLogicalRule(ctx, r, skipCfg, depth+1); err != nil {
								return nil, false, err
							}
						default:
							return nil, false, fmt.Errorf("srs: unknown sub-rule type %d", srt)
						}
					}
					var invert uint8
					if err := binary.Read(r, binary.BigEndian, &invert); err != nil {
						return nil, false, err
					}
					return nil, true, nil
				}
				allGroups = append(allGroups, cidrs)
			case 1:
				cidrs, hasConstraint, err := parseLogicalRule(ctx, r, cfg, depth+1)
				if err != nil {
					return nil, false, err
				}
				if hasConstraint || len(cidrs) == 0 {
					// Skip entire AND. Consume remaining sub-rules and invert byte.
					skipCfg := &ParseSRSConfig{}
					for j := i + 1; j < subCount; j++ {
						srt, skipErr := readByte(r)
						if skipErr != nil {
							return nil, false, skipErr
						}
						switch srt {
						case 0:
							if _, _, err := parseDefaultRule(ctx, r, skipCfg); err != nil {
								return nil, false, err
							}
						case 1:
							if _, _, err := parseLogicalRule(ctx, r, skipCfg, depth+1); err != nil {
								return nil, false, err
							}
						default:
							return nil, false, fmt.Errorf("srs: unknown sub-rule type %d", srt)
						}
					}
					var invert uint8
					if err := binary.Read(r, binary.BigEndian, &invert); err != nil {
						return nil, false, err
					}
					return nil, true, nil
				}
				allGroups = append(allGroups, cidrs)
			default:
				return nil, false, fmt.Errorf("srs: unknown sub-rule type %d", rt)
			}
		}
		var invert uint8
		if err := binary.Read(r, binary.BigEndian, &invert); err != nil {
			return nil, false, err
		}
		if invert != 0 {
			return nil, true, nil
		}
		if len(allGroups) == 0 {
			return nil, false, nil
		}
		result := intersectCIDRs(allGroups...)
		return result, false, nil
	}

	// OR mode (1): collect CIDRs from unconstrained sub-rules (union).
	// OR is constrained only if ALL sub-rules are constrained.
	var allCIDRs []string
	allConstrained := true
	for i := uint64(0); i < subCount; i++ {
		rt, err := readByte(r)
		if err != nil {
			return nil, false, err
		}
		switch rt {
		case 0:
			cidrs, subConstraint, err := parseDefaultRule(ctx, r, cfg)
			if err != nil {
				return nil, false, err
			}
			if !subConstraint {
				allCIDRs = append(allCIDRs, cidrs...)
				allConstrained = false
			}
		case 1:
			cidrs, subConstraint, err := parseLogicalRule(ctx, r, cfg, depth+1)
			if err != nil {
				return nil, false, err
			}
			if !subConstraint {
				allCIDRs = append(allCIDRs, cidrs...)
				allConstrained = false
			}
		default:
			return nil, false, fmt.Errorf("srs: unknown sub-rule type %d", rt)
		}
	}
	var invert uint8
	if err := binary.Read(r, binary.BigEndian, &invert); err != nil {
		return nil, false, err
	}
	if invert != 0 {
		return nil, true, nil // skip inverted logical rules
	}
	return allCIDRs, allConstrained, nil
}
