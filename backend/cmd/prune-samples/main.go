// Command prune-samples xoá file audio mẫu không còn dòng CSDL nào trỏ tới.
//
// Vì sao có file mồ côi: audio mẫu đặt tên theo id của dòng sinh ra nó
// (`sample/assessment/<question id>.mp3`). Dòng bị xoá hoặc đổi id thì file ở lại, không
// ai tham chiếu, và tốn dung lượng mãi mãi. Đo trên máy dev: 31 file mồ côi trên 60.
//
// MẶC ĐỊNH LÀ CHẠY KHÔ. Phải truyền `-delete` mới thực sự xoá:
//
//	go run ./cmd/prune-samples            # chỉ liệt kê
//	go run ./cmd/prune-samples -delete    # xoá thật
//
// CHỈ đụng vào bucket MẪU. Bản ghi âm của người dùng nằm ở bucket khác
// (`S3_BUCKET_RECORDINGS`) và lệnh này không bao giờ liệt kê bucket đó — không phải vì
// bộ lọc nào, mà vì nó không có tên bucket ấy trong tay khi quét.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/phonara/backend/internal/config"
	storedb "github.com/phonara/backend/internal/store/db"
)

// referenceColumns liệt kê MỌI cột có thể trỏ vào bucket mẫu.
//
// Đây là phần nguy hiểm nhất của lệnh này: bỏ sót một cột nghĩa là xoá file đang sống.
// Có test đối chiếu danh sách này với information_schema để cột mới thêm sau không bị
// quên trong im lặng.
var referenceColumns = []struct{ table, column string }{
	{"assessment_questions", "sample_audio_url"},
	{"content_items", "audio_url_us"},
	{"content_items", "audio_url_uk"},
	{"minimal_pairs", "audio_a_us"},
	{"minimal_pairs", "audio_a_uk"},
	{"minimal_pairs", "audio_b_us"},
	{"minimal_pairs", "audio_b_uk"},
	{"passage_sentences", "native_audio_url"},
	{"fix_guides", "media_url"},
	// `daily_challenges.banner_url` hiện trỏ CDN ngoài nên `storageKey` bỏ qua; giữ ở đây
	// để nếu sau này banner được lưu vào bucket thì nó đã được tính là tham chiếu sẵn.
	{"daily_challenges", "banner_url"},
}

func main() {
	doDelete := flag.Bool("delete", false, "xoá thật (mặc định chỉ liệt kê)")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	if err := run(context.Background(), *doDelete); err != nil {
		slog.Error("prune-samples", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, doDelete bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	bucket := cfg.S3.SampleBucket
	if bucket == "" {
		return fmt.Errorf("S3_BUCKET_SAMPLES rỗng — không biết quét bucket nào")
	}
	if bucket == cfg.S3.RecordingBucket {
		// Cấu hình trỏ hai bucket vào cùng một chỗ thì lệnh này sẽ xoá bản ghi âm của
		// người dùng. Dừng, không đoán.
		return fmt.Errorf("S3_BUCKET_SAMPLES và S3_BUCKET_RECORDINGS trùng nhau (%q) — "+
			"từ chối chạy để khỏi xoá nhầm bản ghi người dùng", bucket)
	}

	pool, err := storedb.NewPool(ctx, cfg.DB)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer pool.Close()

	referenced, err := collectReferenced(ctx, pool)
	if err != nil {
		return err
	}

	// CHỐT AN TOÀN. Không có tham chiếu nào nghĩa là hoặc CSDL trống, hoặc truy vấn sai,
	// hoặc trỏ nhầm database. Cả ba trường hợp đều dẫn tới "mọi file đều mồ côi" — tức
	// xoá sạch bucket. Sự cố đó không lùi được, nên coi tập rỗng là lỗi.
	if len(referenced) == 0 {
		return fmt.Errorf("không có dòng nào trỏ tới file mẫu — nghi cấu hình sai " +
			"(sai database?). Từ chối chạy: nếu đúng thật thì mọi file đều bị coi là mồ côi")
	}

	client, err := newMinio(cfg)
	if err != nil {
		return err
	}

	objects, err := listObjects(ctx, client, bucket)
	if err != nil {
		return err
	}

	present := make(map[string]struct{}, len(objects))
	for _, o := range objects {
		present[o.key] = struct{}{}
	}

	var orphans []object
	for _, o := range objects {
		if _, ok := referenced[o.key]; !ok {
			orphans = append(orphans, o)
		}
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].key < orphans[j].key })

	// Chiều ngược lại: dòng CSDL trỏ tới file KHÔNG tồn tại. Không liên quan tới việc xoá,
	// nhưng đây đúng là triệu chứng của lỗi vừa sửa (seed đổi id làm đứt liên kết), và
	// người vận hành cần biết vì nó làm nút "Nghe mẫu" chết trên client.
	var dangling []string
	for key := range referenced {
		if _, ok := present[key]; !ok {
			dangling = append(dangling, key)
		}
	}
	sort.Strings(dangling)

	var freed int64
	for _, o := range orphans {
		freed += o.size
	}

	slog.Info("quét xong",
		"bucket", bucket,
		"object", len(objects),
		"được_tham_chiếu", len(referenced),
		"mồ_côi", len(orphans),
		"giải_phóng_KB", freed/1024)

	for _, o := range orphans {
		fmt.Printf("  mồ côi  %-60s %6d B\n", o.key, o.size)
	}
	for _, k := range dangling {
		fmt.Printf("  THIẾU FILE (CSDL trỏ tới nhưng không có)  %s\n", k)
	}

	if len(dangling) > 0 {
		slog.Warn("có tham chiếu chết — chạy `seed -tts` để sinh lại",
			"số_lượng", len(dangling))
	}

	if !doDelete {
		if len(orphans) > 0 {
			slog.Info("chạy khô — thêm -delete để xoá thật")
		}
		return nil
	}

	var removed int
	for _, o := range orphans {
		if err := client.RemoveObject(ctx, bucket, o.key, minio.RemoveObjectOptions{}); err != nil {
			return fmt.Errorf("xoá %s: %w", o.key, err)
		}
		removed++
	}
	slog.Info("đã xoá", "object", removed, "giải_phóng_KB", freed/1024)
	return nil
}

type object struct {
	key  string
	size int64
}

func listObjects(ctx context.Context, client *minio.Client, bucket string) ([]object, error) {
	var out []object
	for o := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
		if o.Err != nil {
			return nil, fmt.Errorf("liệt kê %s: %w", bucket, o.Err)
		}
		out = append(out, object{key: o.Key, size: o.Size})
	}
	return out, nil
}

// collectReferenced đọc mọi cột trỏ tới file mẫu và trả về tập khoá lưu trữ.
func collectReferenced(ctx context.Context, pool *pgxpool.Pool) (map[string]struct{}, error) {
	refs := make(map[string]struct{})
	for _, c := range referenceColumns {
		// Tên bảng/cột là hằng trong mã nguồn, không đến từ đầu vào ngoài — nhưng vẫn ghép
		// chuỗi thì không kiểm được, nên có test khẳng định từng cột tồn tại thật.
		q := fmt.Sprintf(`SELECT %s FROM %s WHERE %s IS NOT NULL AND %s <> ''`,
			c.column, c.table, c.column, c.column)
		rows, err := pool.Query(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("đọc %s.%s: %w", c.table, c.column, err)
		}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return nil, err
			}
			if key := storageKey(v); key != "" {
				refs[key] = struct{}{}
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("đọc %s.%s: %w", c.table, c.column, err)
		}
	}
	return refs, nil
}

// storageKey đưa giá trị trong CSDL về đúng khoá object.
//
// Cùng một file có thể được lưu dưới vài dạng tuỳ đường code ghi nó: khoá trần
// (`sample/...`), URL API (`/v1/media/sample/...`), hoặc có scheme (`s3://sample/...`).
// Chuẩn hoá sai ở đây thì file đang sống bị coi là mồ côi và bị xoá.
//
// HAI KIỂU SAI Ở ĐÂY KHÔNG NGANG NHAU, và hàm này nghiêng có chủ ý.
//
//   - Coi nhầm giá trị lạ thành khoá hợp lệ → giữ lại một file rác. Tốn vài KB.
//   - Không nhận ra một khoá hợp lệ → file đang dùng bị coi là mồ côi và BỊ XOÁ.
//
// Kiểu sai thứ hai không lùi được, nên khi lưỡng lự thì GIỮ. Cụ thể: hễ thấy `/v1/media/`
// là rút khoá, bất kể host — kể cả URL trỏ tới host khác. Một host lạ tình cờ chứa đoạn
// đó chỉ khiến một file sống dai; ngược lại, từ chối nó sẽ xoá mất file thật nếu sau này
// có đường code nào ghi URL tuyệt đối.
//
// Giá trị KHÔNG trông giống khoá lưu trữ (URL ngoài không có `/v1/media/`, tên drawable
// như `ic_word`) trả rỗng: chúng không khớp object nào nên giữ hay bỏ đều vô hại, và trả
// rỗng khiến ý định rõ ràng hơn.
func storageKey(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if i := strings.Index(v, "/v1/media/"); i >= 0 {
		return strings.TrimPrefix(v[i+len("/v1/media/"):], "/")
	}
	if strings.Contains(v, "://") && !strings.HasPrefix(v, "s3://") && !strings.HasPrefix(v, "file://") {
		return ""
	}
	v = strings.TrimPrefix(strings.TrimPrefix(v, "s3://"), "file://")
	return strings.TrimPrefix(v, "/")
}

func newMinio(cfg *config.Config) (*minio.Client, error) {
	endpoint := strings.TrimPrefix(strings.TrimPrefix(cfg.S3.Endpoint, "https://"), "http://")
	secure := strings.HasPrefix(cfg.S3.Endpoint, "https://")
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.S3.AccessKey, cfg.S3.SecretKey, ""),
		Secure: secure,
		Region: cfg.S3.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("kết nối S3 %q: %w", endpoint, err)
	}
	return c, nil
}
