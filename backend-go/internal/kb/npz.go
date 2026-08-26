// ============ 本文件职责中文说明 ============
// 向量索引（.npz 文件）读写与余弦相似度检索：解析 numpy .npy/.npz 二进制格式
// （ids 为 int64 行 ID，vecs 为 float32 二维向量，每行已 L2 归一化）。
// 提供检索时按租户（IDTenants）与目标语言（IDLangs）过滤、top-k 相似度排序、
// 追加/更新向量（UpdateIndex）以及 numpy 兼容格式的写回。
// =============================================
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
	IDs  []int64     // 行 ID 数组（对应知识库条目主键）
	Vecs [][]float32 // 向量矩阵：每行已 L2 归一化，shape (N, 1024)

	// IDLangs rowID → 该行已有的语言集合。用于按目标语言过滤检索。
	// 由外部（Engine）从知识库 DB 构建，加载 npz 后填充。
	IDLangs map[int64]map[string]bool

	// IDTenants rowID → 所属租户 id。语义检索按租户过滤。
	IDTenants map[int64]int64

	// IDPacks rowID → 归属知识库包 id（0=历史兜底）。语义检索按可应用包过滤。
	IDPacks map[int64]int64
}

// SearchResult 相似度搜索结果
type SearchResult struct {
	ID      int64   // 命中的行 ID
	Sim     float64 // 相似度分数（余弦相似度/点积，越高越相似）
	InChain bool    // ★ 是否属于用户链内直接采用域（链内部门包/企业/历史行/共享层）；false=跨部门回退（仅例句参考）
}

// LoadNPZ 解析 numpy .npz 文件（zip 容器内是 .npy 格式）。
// 参数：path=.npz 文件路径；返回索引对象（含 ids 与 vecs，并校验行数一致）。
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
			// 读取 ids.npy 中的 int64 数组
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
			// 读取 vecs.npy 中的 float32 二维数组
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
	// 完整性校验：ids 与 vecs 均不能为空且行数必须一致
	if len(idx.IDs) == 0 || len(idx.Vecs) == 0 {
		return nil, fmt.Errorf("npz 缺少 ids 或 vecs")
	}
	if len(idx.IDs) != len(idx.Vecs) {
		return nil, fmt.Errorf("npz ids(%d) 与 vecs(%d) 行数不一致", len(idx.IDs), len(idx.Vecs))
	}
	return idx, nil
}

// readZipFile 读取 zip 内单个文件的全部字节。
// 参数：f=zip 文件项；返回文件内容字节。
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

// parseNPYHeader 解析 .npy 文件头，返回 descr、shape、数据起始偏移。
// 格式：magic "\x93NUMPY" + version(1B major,1B minor) + header_len + header dict + data。
// 参数：data=.npy 文件字节；返回 dtype 描述、shape、数据偏移。
func parseNPYHeader(data []byte) (descr string, shape []int, offset int, err error) {
	// 校验魔数（前 6 字节固定）
	if len(data) < 10 || !bytes.Equal(data[:6], []byte{0x93, 'N', 'U', 'M', 'P', 'Y'}) {
		return "", nil, 0, fmt.Errorf("不是有效的 .npy 文件")
	}
	major := data[6]
	minor := data[7]
	var headerLen int
	// 按版本解析 header 长度（v1 用 2 字节，v2/v3 用 4 字节）
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

// parseNumpyHeaderDict 从 numpy header 字典字符串中提取 descr 与 shape。
// 参数：h=header 字符串；返回 dtype 描述与 shape 数组。
func parseNumpyHeaderDict(h string) (string, []int, error) {
	// 去除前后空格，允许尾随空格填充
	h = strings.TrimSpace(h)
	h = strings.TrimSuffix(h, " ")
	// 简化：用正则提取 descr 和 shape
	descr := ""
	// 解析 'descr': '<i8' 字段：跳过冒号取引号内的值
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
	// 解析 'shape': (3262, 1024) 字段：取括号内逗号分隔的维度
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

// parseNPYFloat32 解析 float32 二维数组（row-major），兼容 float64。
// 参数：data=.npy 文件字节；返回 [][]float32 矩阵。
func parseNPYFloat32(data []byte) ([][]float32, error) {
	descr, shape, offset, err := parseNPYHeader(data)
	if err != nil {
		return nil, err
	}
	// 支持 '<f4', '|f4', '=f4'（小端 float32）
	if !strings.Contains(descr, "f4") && !strings.Contains(descr, "f8") {
		return nil, fmt.Errorf("descr %q 不是 float 类型", descr)
	}
	// 计算元素总数
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
		// float64：每元素 8 字节，转成 float32
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

	// float32：每元素 4 字节
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

// parseNPYInt64 解析 int64 一维数组。
// 参数：data=.npy 文件字节；返回 int64 数组。
func parseNPYInt64(data []byte) ([]int64, error) {
	descr, shape, offset, err := parseNPYHeader(data)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(descr, "i8") {
		return nil, fmt.Errorf("descr %q 不是 int64 类型", descr)
	}
	// 计算元素总数
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
// 参数：query=查询向量，k=返回条数，wantLangs=目标语言过滤（非空时只返回含任一目标语言的行，nil 表示不过滤），
// tenantID=租户过滤（>0 时只检索该租户的行，0 表示不过滤）。
// 返回：按相似度降序的搜索结果列表。
func (idx *Index) Search(query []float32, k int, wantLangs []string, tenantID int64) []SearchResult {
	return idx.ScopedSearch(query, k, wantLangs, tenantID, nil)
}

// ScopedSearch 带知识库包白名单的相似度检索：allowPacks 非 nil 时仅检索归属其中的行
// （部门包>组织包>行业包>语言文化包 的应用范围由引擎按租户+注册行业计算）。
func (idx *Index) ScopedSearch(query []float32, k int, wantLangs []string, tenantID int64, allowPacks map[int64]bool) []SearchResult {
	if len(query) == 0 || len(idx.Vecs) == 0 {
		return nil // 无查询向量或无索引数据直接返回
	}
	type scored struct {
		id  int64
		sim float64
	}
	results := make([]scored, 0, len(idx.Vecs))
	for i, v := range idx.Vecs {
		if len(v) != len(query) {
			continue // 维度不一致跳过
		}
		// 按租户过滤
		if tenantID > 0 && idx.IDTenants != nil {
			if idx.IDTenants[idx.IDs[i]] != tenantID {
				continue // 非目标租户的行跳过
			}
		}
		// 按知识库包白名单过滤（优先级链的应用范围）
		if allowPacks != nil && idx.IDPacks != nil {
			if !allowPacks[idx.IDPacks[idx.IDs[i]]] && idx.IDPacks[idx.IDs[i]] != 0 {
				continue // 不在可应用包内的行跳过（pack_id=0 的历史行保留兜底）
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
		// 计算点积（归一化向量点积即余弦相似度）
		var dot float32
		for j := range query {
			dot += v[j] * query[j]
		}
		results = append(results, scored{id: idx.IDs[i], sim: float64(dot)})
	}
	// 按相似度降序排序
	sort.Slice(results, func(a, b int) bool { return results[a].sim > results[b].sim })
	if k > len(results) {
		k = len(results) // 防越界
	}
	out := make([]SearchResult, k)
	for i := 0; i < k; i++ {
		out[i] = SearchResult{ID: results[i].id, Sim: results[i].sim}
	}
	return out
}

// SaveNPZ 写回 npz 文件（追加或更新某条记录）。用 numpy 兼容格式。
// 实现：先写 .tmp 临时文件再原子改名，避免中途失败损坏原文件。
// 参数：path=输出路径，ids=行 ID 数组，vecs=向量矩阵。
func SaveNPZ(path string, ids []int64, vecs [][]float32) error {
	tmp := path + ".tmp" // 先写临时文件
	zf, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer zf.Close()
	zw := zip.NewWriter(zf)

	// 写 ids.npy
	if err := writeNPYInt64(zw, "ids", ids); err != nil {
		zw.Close()
		os.Remove(tmp) // 失败清理临时文件
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
	return os.Rename(tmp, path) // 原子替换目标文件
}

// npyHeader 生成 numpy v1.0 规范的 header（含 64 字节对齐填充）。
// 参数：descr=dtype 描述（如 <f4），shape=维度数组；返回 header 字节（含换行结尾）。
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
		sb.WriteString(",") // 一维 shape 末尾补逗号
	}
	sb.WriteString("), }")
	// pad to 64-byte alignment (v1.0 规范)
	header := []byte(sb.String())
	pad := 64 - (len(header)+1)%64 // 计算补齐空格数（含结尾换行）
	for i := 0; i < pad; i++ {
		header = append(header, ' ')
	}
	header = append(header, '\n')
	return header
}

// writeNPYFloat32 把 float32 二维矩阵写入 zip 项（numpy 兼容格式）。
// 参数：zw=zip 写入器，name=zip 内文件名，vecs=向量矩阵。
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
	magic := []byte{0x93, 'N', 'U', 'M', 'P', 'Y', 1, 0} // v1.0 魔数
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
	// 逐元素以小端 float32 写数据
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

// writeNPYInt64 把 int64 一维数组写入 zip 项（numpy 兼容格式）。
// 参数：zw=zip 写入器，name=zip 内文件名，ids=行 ID 数组。
func writeNPYInt64(zw *zip.Writer, name string, ids []int64) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	header := npyHeader("<i8", []int{len(ids)})
	magic := []byte{0x93, 'N', 'U', 'M', 'P', 'Y', 1, 0} // v1.0 魔数
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
	// 逐元素以小端 int64 写数据
	buf := make([]byte, 8)
	for _, id := range ids {
		binary.LittleEndian.PutUint64(buf, uint64(id))
		if _, err := w.Write(buf); err != nil {
			return err
		}
	}
	return nil
}

// UpdateIndex 更新索引：更新/追加一条记录的向量。
// 参数：id=行 ID，vec=新向量（已归一化）；已存在则覆盖，否则追加。
func (idx *Index) Update(id int64, vec []float32) {
	for i, v := range idx.IDs {
		if v == id {
			idx.Vecs[i] = vec // 已存在：覆盖该行向量
			return
		}
	}
	// 不存在：追加新行
	idx.IDs = append(idx.IDs, id)
	idx.Vecs = append(idx.Vecs, vec)
}

// GetVec 返回指定 id 的向量。
// 参数：id=行 ID；返回对应向量（不存在返回 nil）。
func (idx *Index) GetVec(id int64) []float32 {
	for i, v := range idx.IDs {
		if v == id {
			return idx.Vecs[i]
		}
	}
	return nil
}

// ============ 组织继承链 Scoped 向量检索（2026-08-26《KB组织继承链与部门隔离改造方案》） ============

// ScopedSearchScope 按 PackScope 可见范围的向量检索（ScopedSearch 的继承链版）。
// ★ 修复审计 #10：旧实现按「行租户==请求租户」一刀切，宿主在租户1 的共享行业/文化包
//   永远不可召回——现改为 scope 集合判定：
//     直接采用域(InChain=true)：本租户的 历史行0/企业包/链内部门包 + 共享行业/文化包
//     跨部门回退域(InChain=false)：租户开关开时的其他部门共享包（调用方仅可作例句）
func (idx *Index) ScopedSearchScope(query []float32, k int, wantLangs []string, tenantID int64, scope *PackScope) []SearchResult {
	if len(query) == 0 || len(idx.Vecs) == 0 {
		return nil // 无查询向量或无索引数据直接返回
	}
	type scored struct {
		id      int64
		sim     float64
		inChain bool
	}
	results := make([]scored, 0, len(idx.Vecs))
	for i, v := range idx.Vecs {
		if len(v) != len(query) {
			continue // 维度不一致跳过
		}
		rowID := idx.IDs[i]
		pack := int64(0)
		if idx.IDPacks != nil {
			pack = idx.IDPacks[rowID]
		}
		rowTenant := tenantID
		if idx.IDTenants != nil {
			rowTenant = idx.IDTenants[rowID]
		}
		// ★ 可见性三选一（scope=nil 时退化为旧租户相等口径）
		inChain := false
		visible := false
		if scope == nil {
			visible = tenantID <= 0 || rowTenant == tenantID
			inChain = visible
		} else {
			_, chainOK := scope.ChainPacks[pack]
			switch {
			case rowTenant == tenantID && (pack == 0 || scope.TenantPackIDs[pack] || chainOK):
				visible, inChain = true, true // 直接采用域
			case scope.SharedPackIDs[pack]:
				visible, inChain = true, true // 共享层同属采用域
			case scope.AllowCrossDept && func() bool { _, ok := scope.CrossDeptPacks[pack]; return ok }():
				visible = true // 跨部门回退域（仅例句参考）
			}
		}
		if !visible {
			continue
		}
		// 按目标语言过滤：该行不含任一目标语言则跳过
		if len(wantLangs) > 0 && idx.IDLangs != nil {
			has := false
			for _, lc := range wantLangs {
				if idx.IDLangs[rowID][lc] {
					has = true
					break
				}
			}
			if !has {
				continue
			}
		}
		// 计算点积（归一化向量点积即余弦相似度）
		var dot float32
		for j := range query {
			dot += v[j] * query[j]
		}
		results = append(results, scored{id: rowID, sim: float64(dot), inChain: inChain})
	}
	// 排序策略：链内优先于跨部门；同层按相似度降序——保证链内弱相似候选仍排在
	// 跨部门强相似之前（隔离语义在排序层的最后防线），引擎侧再按阈值分流。
	sort.Slice(results, func(a, b int) bool {
		if results[a].inChain != results[b].inChain {
			return results[a].inChain
		}
		return results[a].sim > results[b].sim
	})
	if k > len(results) {
		k = len(results)
	}
	out := make([]SearchResult, k)
	for i := 0; i < k; i++ {
		out[i] = SearchResult{ID: results[i].id, Sim: results[i].sim, InChain: results[i].inChain}
	}
	return out
}
