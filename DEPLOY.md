# Deploy lên VPS

Cho SSD Nodes hoặc bất kỳ VPS Linux nào. Đã kiểm với 8 GB RAM.

Bản dev (`docker-compose.yml`) **không dùng được cho internet**: nó mở cổng Postgres, Redis
và MinIO ra mọi interface với mật khẩu mặc định nằm sẵn trong git. Dùng
`docker-compose.prod.yml`.

## Khác biệt giữa hai bản

| | dev | production |
|---|---|---|
| Cổng lộ ra | 5432, 6379, 9000, 9001, 8080 | **chỉ 80 và 443** |
| Mật khẩu | ghi cứng trong compose | bắt buộc từ `.env`, thiếu là báo lỗi ngay |
| Redis | không mật khẩu | `requirepass` |
| HTTPS | không | Caddy tự xin Let's Encrypt |
| Giới hạn RAM | không | có, theo từng container |
| Log | không giới hạn | xoay vòng 10 MB × 3 |

Redis không mật khẩu mà mở ra internet thì **mất máy trong vài giờ** — bot quét cổng 6379
rồi ghi khoá SSH qua `CONFIG SET`. Đây là lý do bản prod tách hẳn thành file riêng: khi gộp
nhiều file, Compose **cộng dồn** `ports` chứ không thay thế, nên không có cách nào đóng cổng
đã mở ở file gốc.

---

## Bước 1 — Chuẩn bị VPS

Kiểm phiên bản trước — lệnh cài khác nhau:

```bash
. /etc/os-release && echo "$PRETTY_NAME" && uname -m
```

**Ubuntu 22.04 trở lên:**

```bash
sudo apt update && sudo apt install -y docker.io docker-compose-v2 git
```

**Ubuntu 20.04** — gói `docker-compose-v2` chưa có trong kho, phải dùng kho chính thức của
Docker. Cài `docker.io` từ kho Ubuntu rồi thêm plugin compose sẽ **xung đột phiên bản**, nên
cài trọn bộ từ một nguồn:

```bash
curl -fsSL https://get.docker.com | sudo sh && sudo apt install -y git
```

Script đó cài `docker-ce` kèm `docker compose` v2. Kiểm:

```bash
docker --version && docker compose version
```

Không ra `Docker Compose version v2.x` thì dừng lại — phần còn lại của hướng dẫn dùng cú
pháp `docker compose` (có dấu cách), không phải `docker-compose` cũ.

Nếu đăng nhập bằng user thường chứ không phải root:

```bash
sudo usermod -aG docker $USER
```

Rồi đăng xuất, đăng nhập lại để nhóm `docker` có hiệu lực.

Tường lửa — chỉ mở SSH và web:

```bash
sudo ufw allow 22 && sudo ufw allow 80 && sudo ufw allow 443 && sudo ufw --force enable
```

> `ufw` là lớp phòng thủ **thứ hai**. Docker tự thêm luật iptables và có thể đi vòng qua
> ufw, nên lớp phòng thủ thật là việc bản prod không khai `ports` cho các dịch vụ nội bộ.

## Bước 2 — Trỏ tên miền

Tạo bản ghi A trỏ về IP VPS, **trước khi** khởi động. Let's Encrypt xác minh bằng cách gọi
ngược về tên miền; chưa trỏ thì Caddy thử lại liên tục và có thể dính giới hạn tần suất.

```bash
dig +short api.phonara.online
```

Ra đúng IP VPS thì đi tiếp.

## Bước 3 — Lấy mã nguồn và tạo `.env`

```bash
git clone <repo> && cd Phonara_Backend/backend && cp .env.prod.example .env && chmod 600 .env
```

Sinh khoá bí mật:

```bash
for k in JWT_ACCESS_SECRET JWT_REFRESH_SECRET DB_PASSWORD REDIS_PASSWORD S3_SECRET_KEY; do echo "$k=$(openssl rand -base64 32 | tr -d '/+=' | head -c 40)"; done
```

Dán vào `.env`, rồi điền `DOMAIN`, `TLS_EMAIL`, `AZURE_TTS_KEY`.

**Không dùng lại mật khẩu của máy dev.** Chúng nằm công khai trong `docker-compose.yml` ở
git — coi như đã lộ.

## Bước 4 — Build

```bash
cd /root/Phonara_Backend/backend && docker compose -f docker-compose.prod.yml build
```

Image engine nặng **5,4 GB** vì có PyTorch và model nướng sẵn. Lần build đầu tải khoảng
3,2 GB — mất 10–20 phút tuỳ đường truyền.

> **Không copy image từ máy Mac sang.** Máy phát triển là `linux/arm64` (Apple Silicon),
> VPS gần như chắc chắn là `linux/amd64`. Bánh xe PyTorch và mọi layer đều theo kiến trúc.
> Phải build tại chỗ.

## Bước 5 — Khởi động

```bash
cd /root/Phonara_Backend/backend && docker compose -f docker-compose.prod.yml up -d
```

Lần đầu engine mất tới 2 phút để nạp model và warm-up — `start_period` đã tính chuyện đó.

```bash
cd /root/Phonara_Backend/backend && docker compose -f docker-compose.prod.yml ps
```

Chờ tới khi mọi dịch vụ `healthy`.

## Bước 6 — Migration và dữ liệu

```bash
cd /root/Phonara_Backend/backend && docker compose -f docker-compose.prod.yml --profile migrate run --rm migrate
```

```bash
cd /root/Phonara_Backend/backend && docker compose -f docker-compose.prod.yml --profile seed run --rm seed
```

Sinh audio mẫu — chỉ chạy khi đã điền `AZURE_TTS_KEY`, và **tốn hạn mức Azure**:

```bash
cd /root/Phonara_Backend/backend && docker compose -f docker-compose.prod.yml --profile seed run --rm seed -tts
```

Lệnh này chỉ đẩy task vào hàng đợi; worker sinh audio nền, xem tiến độ ở log worker. Với
~1.800 ký tự nội dung hiện tại thì xong trong vài phút.

## Bước 7 — Kiểm tra

```bash
curl -s https://api.phonara.online/health
```

```bash
curl -s -X POST https://api.phonara.online/v1/auth/guest -H 'Content-Type: application/json' -d '{"device_id":"deploy-check"}' | head -c 120
```

Ra `access_token` là toàn bộ đường đi đã thông: Caddy → api → Postgres → Redis.

**Kiểm luôn rằng cổng nội bộ KHÔNG lộ ra** — chạy từ máy khác, không phải từ VPS:

```bash
nmap -Pn -p 22,80,443,5432,6379,9000,9001,8080 api.phonara.online
```

Chỉ 22, 80, 443 được `open`. Thấy 5432 hay 6379 mở là dừng lại xử lý ngay.

## Bước 8 — Trỏ app về server

`shared/data/.../config/Environment.kt` đang ghi cứng `http://localhost:8080/`. Đổi
`baseUrl` của môi trường staging/prod thành `https://api.phonara.online/`.

Có HTTPS rồi thì bản **release** gọi được — Android chỉ chặn cleartext HTTP.

---

## Sao lưu

Không có sao lưu thì một lệnh `docker compose down -v` gõ nhầm là mất sạch dữ liệu người
dùng. Postgres là thứ duy nhất không tái tạo được — MinIO thì sinh lại được từ TTS, mã nguồn
thì ở git.

```bash
mkdir -p ~/backups && cd /root/Phonara_Backend/backend && docker compose -f docker-compose.prod.yml exec -T postgres pg_dump -U phonara phonara | gzip > ~/backups/phonara-$(date +%F).sql.gz
```

Đặt vào cron hằng ngày:

```bash
crontab -l 2>/dev/null | { cat; echo "0 3 * * * cd /root/Phonara_Backend/backend && docker compose -f docker-compose.prod.yml exec -T postgres pg_dump -U phonara phonara | gzip > ~/backups/phonara-\$(date +\%F).sql.gz"; } | crontab -
```

Sao lưu chưa thử phục hồi thì chưa phải sao lưu. Thử một lần trên máy dev.

Volume `caddydata` chứa chứng chỉ Let's Encrypt — mất thì phải xin lại, và có giới hạn tần
suất. Nên sao lưu kèm.

## Cập nhật mã

```bash
cd /root/Phonara_Backend/backend && git pull && docker compose -f docker-compose.prod.yml up -d --build
```

Đổi mã engine thì lần build lại vẫn nhanh: layer PyTorch và model được cache.

## Xem log

```bash
cd /root/Phonara_Backend/backend && docker compose -f docker-compose.prod.yml logs -f --tail=100 api worker
```

---

## Còn thiếu ở bản deploy này

| | |
|---|---|
| Thanh toán IAP | trả 503 — chưa xây |
| Đăng nhập Google/Apple | trả 503 — chưa xây |
| Chấm bài thi nói | trả 503 — engine không chấm được nói tự do |
| Xuất dữ liệu GDPR | trả 501 |
| Giám sát / cảnh báo | chưa có. Container chết thì `restart: unless-stopped` tự dựng lại, nhưng không ai được báo |
| Điểm số phát âm | `calibration.json` còn hậu tố `-PLACEHOLDER` — **thứ tự tương đối đáng tin, con số tuyệt đối thì chưa** |

Mục cuối là quan trọng nhất nếu cho người thật dùng: điểm hiển thị chưa được hiệu chỉnh
trên dữ liệu nào. Xem giai đoạn 4 của `PRONUNCIATION_ENGINE_PLAN.md`.
