package kb

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Index 向量索引：等价于 numpy 的 (ids, vecs)
type Index struct {
	IDs  []int64
	Vecs [][]float32 // 每行已 L2 归一化，shape (N, 1024)

	// IDLangs rowID → 该行已有的语言集合。用于按目标语言过滤检索。
	// 由外部（Engine）从知识库 DB 构建，加载 npz 后填充。
	IDLangs map[int64]map[string]bool

	// IDTenants rowID → 所属租户 id。语义检索按租户过滤。
	IDTenants map[int64]int64
}

// SearchResult 相似度搜索结果
type SearchResult struct {
	ID  int64
	Sim float64
}

// LoadNPZ 解析 numpy .npz 文件（zip 容器内是 .npy 格式）
func LoadNPZ(path string) (*Index, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	idx := &Index{}
	for _, f := range zr.File {
		name := strings.TrimSuffix(f.Name, ".npy")
		switch name {
		case "ids":
			data, err := readZipFile(f)
			if err != nil {
				return nil, err
			}
			ids, err := parseNPYInt64(data)
			if err != nil {
				return nil, fmt.Errorf("parse ids: %w", err)
			}
			idx.IDs = ids
		case "vecs":
			data, err := readZipFile(f)
			if err != nil {
				return nil, err
			}
			vecs, err := parseNPYFloat32(data)
			if err != nil {
				return nil, fmt.Errorf("parse vecs: %w", err)
			}
			idx.Vecs = vecs
		}
	}
	if len(idx.IDs) == 0 || len(idx.Vecs) == 0 {
		return nil, fmt.Errorf("npz 缺少 ids 或 vecs")
	}
	if len(idx.IDs) != len(idx.Vecs) {
		return nil, fmt.Errorf("npz ids(%d) 与 vecs(%d) 行数不一致", len(idx.IDs), len(idx.Vecs))
	}
	return idx, nil
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(rc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// parseNPYHeader 解析 .npy 文件头，返回 descr、shape、数据起始偏移
// 格式：magic "\x93NUMPY" + version(1B major,1B minor) + header_len + header dict + data
func parseNPYHeader(data []byte) (descr string, shape []int, offset int, err error) {
	if len(data) < 10 || !bytes.Equal(data[:6], []byte{0x93, 'N', 'U', 'M', 'P', 'Y'}) {
		return "", nil, 0, fmt.Errorf("不是有效的 .npy 文件")
	}
	major := data[6]
	minor := data[7]
	var headerLen int
	switch major {
	case 1:
		if len(data) < 10 {
			return "", nil, 0, fmt.Errorf("header 长度不足")
		}
		headerLen = int(binary.LittleEndian.Uint16(data[8:10]))
		offset = 10
	case 2, 3:
		if len(data) < 12 {
			return "", nil, 0, fmt.Errorf("header 长度不足")
		}
		headerLen = int(binary.LittleEndian.Uint32(data[8:12]))
		offset = 12
	default:
		return "", nil, 0, fmt.Errorf("不支持 .npy 版本 %d.%d", major, minor)
	}
	if offset+headerLen > len(data) {
		return "", nil, 0, fmt.Errorf("header 越界")
	}
	header := string(data[offset : offset+headerLen])
	// header 形如 {'descr': '<f4', 'fortran_order': False, 'shape': (3262, 1024), }
	descr, shape, err = parseNumpyHeaderDict(header)
	if err != nil {
		return "", nil, 0, err
	}
	offset += headerLen
	return descr, shape, offset, nil
}

func parseNumpyHeaderDict(h string) (string, []int, error) {
	// 去除前后空格，允许尾随空格填充
	h = strings.TrimSpace(h)
	h = strings.TrimSuffix(h, " ")
	// 简化：用正则提取 descr 和 shape
	descr := ""
	if i := strings.Index(h, "'descr'"); i >= 0 {
		rest := h[i+len("'descr'"):]
		// 期望 'descr': '<i8' 的形式，跳过 冒号[:space:]然后取引号内的值
		colon := strings.Index(rest, ":")
		if colon >= 0 {
			rest = rest[colon+1:]
			rest = strings.TrimSpace(rest)
			if strings.HasPrefix(rest, "'") {
				rest = rest[1:]
				if k := strings.Index(rest, "'"); k >= 0 {
					descr = rest[:k]
				}
			} else if strings.HasPrefix(rest, "\"") {
				rest = rest[1:]
				if k := strings.Index(rest, "\""); k >= 0 {
					descr = rest[:k]
				}
			}
		}
	}
	shape := []int{}
	if i := strings.Index(h, "'shape'"); i >= 0 {
		rest := h[i:]
		if j := strings.Index(rest, "("); j >= 0 {
			shapePart := rest[j:]
			if k := strings.Index(shapePart, ")"); k >= 0 {
				shapePart = shapePart[1:k]
				for _, s := range strings.Split(shapePart, ",") {
					s = strings.TrimSpace(s)
					if s == "" {
						continue
					}
					n, err := strconv.Atoi(s)
					if err != nil {
						return "", nil, fmt.Errorf("解析 shape 元素 %q: %w", s, err)
					}
					shape = append(shape, n)
				}
			}
		}
	}
	if descr == "" || len(shape) == 0 {
		return "", nil, fmt.Errorf("无法解析 numpy header: %s", h)
	}
	return descr, shape, nil
}

// parseNPYFloat32 解析 float32 数组（row-major）
func parseNPYFloat32(data []byte) ([][]float32, error) {
	descr, shape, offset, err := parseNPYHeader(data)
	if err != nil {
		return nil, err
	}
	// 支持 '<f4', '|f4', '=f4'（小端 float32）
	if !strings.Contains(descr, "f4") && !strings.Contains(descr, "f8") {
		return nil, fmt.Errorf("descr %q 不是 float 类型", descr)
	}
	elems := 1
	for _, d := range shape {
		elems *= d
	}
	if len(shape) < 1 || len(shape) > 2 {
		return nil, fmt.Errorf("不支持 shape %v", shape)
	}
	rows := shape[0]
	cols := 1
	if len(shape) == 2 {
		cols = shape[1]
	}

	if strings.Contains(descr, "f8") {
		// float64
		need := elems * 8
		if offset+need > len(data) {
			return nil, fmt.Errorf("数据不足")
		}
		vals := data[offset : offset+need]
		out := make([][]float32, rows)
		for r := 0; r < rows; r++ {
			out[r] = make([]float32, cols)
			for c := 0; c < cols; c++ {
				idx := (r*cols + c) * 8
				bits := binary.LittleEndian.Uint64(vals[idx : idx+8])
				f := math.Float64frombits(bits)
				// 同时兼容大端：检测字节序
				out[r][c] = float32(f)
			}
		}
		return out, nil
	}

	need := elems * 4
	if offset+need > len(data) {
		return nil, fmt.Errorf("数据不足: 需要 %d 字节, 实际 %d", need, len(data)-offset)
	}
	vals := data[offset : offset+need]
	out := make([][]float32, rows)
	for r := 0; r < rows; r++ {
		out[r] = make([]float32, cols)
		for c := 0; c < cols; c++ {
			idx := (r*cols + c) * 4
			bits := binary.LittleEndian.Uint32(vals[idx : idx+4])
			out[r][c] = math.Float32frombits(bits)
		}
	}
	return out, nil
}

// parseNPYInt64 解析 int64 数组（1D）
func parseNPYInt64(data []byte) ([]int64, error) {
	descr, shape, offset, err := parseNPYHeader(data)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(descr, "i8") {
		return nil, fmt.Errorf("descr %q 不是 int64 类型", descr)
	}
	elems := 1
	for _, d := range shape {
		elems *= d
	}
	need := elems * 8
	if offset+need > len(data) {
		return nil, fmt.Errorf("数据不足")
	}
	vals := data[offset : offset+need]
	out := make([]int64, elems)
	for i := 0; i < elems; i++ {
		idx := i * 8
		out[i] = int64(binary.LittleEndian.Uint64(vals[idx : idx+8]))
	}
	return out, nil
}

// Search 余弦相似度搜索（向量已归一化 → 点积）。返回降序 top k。
// wantLangs 非空时只返回含任一目标语言的行；nil 表示不按语言过滤（全量）。
// tenantID > 0 时只检索该租户的行（0 表示不按租户过滤）。
func (idx *Index) Search(query []float32, k int, wantLangs []string, tenantID int64) []SearchResult {
	if len(query) == 0 || len(idx.Vecs) == 0 {
		return nil
	}
	type scored struct {
		id  int64
		sim float64
	}
	results := make([]scored, 0, len(idx.Vecs))
	for i, v := range idx.Vecs {
		if len(v) != len(query) {
			continue
		}
		// 按租户过滤
		if tenantID > 0 && idx.IDTenants != nil {
			if idx.IDTenants[idx.IDs[i]] != tenantID {
				continue
			}
		}
		// 按目标语言过滤：该行不含任一目标语言则跳过，不参与检索
		if len(wantLangs) > 0 && idx.IDLangs != nil {
			has := false
			for _, lc := range wantLangs {
				if idx.IDLangs[idx.IDs[i]][lc] {
					has = true
					break
				}
			}
			if !has {
				continue
			}
		}
		var dot float32
		for j := range query {
			dot += v[j] * query[j]
		}
		results = append(results, scored{id: idx.IDs[i], sim: float64(dot)})
	}
	sort.Slice(results, func(a, b int) bool { return results[a].sim > results[b].sim })
	if k > len(results) {
		k = len(results)
	}
	out := make([]SearchResult, k)
	for i := 0; i < k; i++ {
		out[i] = SearchResult{ID: results[i].id, Sim: results[i].sim}
	}
	return out
}

// SaveNPZ 写回 npz 文件（追加或更新某条记录）。用 numpy 兼容格式。
func SaveNPZ(path string, ids []int64, vecs [][]float32) error {
	tmp := path + ".tmp"
	zf, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer zf.Close()
	zw := zip.NewWriter(zf)

	// 写 ids.npy
	if err := writeNPYInt64(zw, "ids", ids); err != nil {
		zw.Close()
		os.Remove(tmp)
		return err
	}
	// 写 vecs.npy
	if err := writeNPYFloat32(zw, "vecs", vecs); err != nil {
		zw.Close()
		os.Remove(tmp)
		return err
	}
	if err := zw.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := zf.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func npyHeader(descr string, shape []int) []byte {
	var sb strings.Builder
	sb.WriteString("{'descr': '")
	sb.WriteString(descr)
	sb.WriteString("', 'fortran_order': False, 'shape': (")
	for i, s := range shape {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(strconv.Itoa(s))
	}
	if len(shape) == 1 {
		sb.WriteString(",")
	}
	sb.WriteString("), }")
	// pad to 64-byte alignment (v1.0 规范)
	header := []byte(sb.String())
	pad := 64 - (len(header)+1)%64
	for i := 0; i < pad; i++ {
		header = append(header, ' ')
	}
	header = append(header, '\n')
	return header
}

func writeNPYFloat32(zw *zip.Writer, name string, vecs [][]float32) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	rows := len(vecs)
	cols := 0
	if rows > 0 {
		cols = len(vecs[0])
	}
	header := npyHeader("<f4", []int{rows, cols})
	magic := []byte{0x93, 'N', 'U', 'M', 'P', 'Y', 1, 0}
	hlen := []byte{byte(len(header) & 0xff), byte((len(header) >> 8) & 0xff)}
	if _, err := w.Write(magic); err != nil {
		return err
	}
	if _, err := w.Write(hlen); err != nil {
		return err
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	buf := make([]byte, 4)
	for _, v := range vecs {
		for _, x := range v {
			binary.LittleEndian.PutUint32(buf, math.Float32bits(x))
			if _, err := w.Write(buf); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeNPYInt64(zw *zip.Writer, name string, ids []int64) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	header := npyHeader("<i8", []int{len(ids)})
	magic := []byte{0x93, 'N', 'U', 'M', 'P', 'Y', 1, 0}
	hlen := []byte{byte(len(header) & 0xff), byte((len(header) >> 8) & 0xff)}
	if _, err := w.Write(magic); err != nil {
		return err
	}
	if _, err := w.Write(hlen); err != nil {
		return err
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	buf := make([]byte, 8)
	for _, id := range ids {
		binary.LittleEndian.PutUint64(buf, uint64(id))
		if _, err := w.Write(buf); err != nil {
			return err
		}
	}
	return nil
}

// UpdateIndex 更新索引：更新/追加一条记录的向量
func (idx *Index) Update(id int64, vec []float32) {
	for i, v := range idx.IDs {
		if v == id {
			idx.Vecs[i] = vec
			return
		}
	}
	idx.IDs = append(idx.IDs, id)
	idx.Vecs = append(idx.Vecs, vec)
}

// GetVec 返回指定 id 的向量
func (idx *Index) GetVec(id int64) []float32 {
	for i, v := range idx.IDs {
		if v == id {
			return idx.Vecs[i]
		}
	}
	return nil
}
