# R1 — kiểm chứng vocab. Chạy trong container để không đụng máy dev và để kiểm đúng
# phiên bản espeak-ng mà production sẽ pin (§3.4, R10).
FROM python:3.11-slim

# espeak-ng là binary hệ thống mà phonemizer gọi qua. Phiên bản của nó quyết định chuỗi
# phoneme sinh ra → phải ghi lại và pin ở production Dockerfile (§8, g2p_version).
RUN apt-get update \
 && apt-get install -y --no-install-recommends espeak-ng \
 && rm -rf /var/lib/apt/lists/* \
 && echo "─── phiên bản espeak-ng đã cài ───" \
 && espeak-ng --version

# transformers cần cho Wav2Vec2PhonemeCTCTokenizer; KHÔNG cài torch — R1 chỉ dùng
# tokenizer (pure Python), không chạy inference.
RUN pip install --no-cache-dir \
      "transformers==4.46.3" \
      "phonemizer==3.3.0"

WORKDIR /r1
COPY sentences.py check_vocab.py ./

# KHÔNG hardcode đường dẫn thư viện espeak — nó phụ thuộc kiến trúc
# (/usr/lib/aarch64-linux-gnu/ trên arm64, /usr/lib/x86_64-linux-gnu/ trên amd64).
# phonemizer tự tìm qua dlinfo; hardcode sẽ làm image chỉ chạy được trên một kiến trúc.
CMD ["python", "check_vocab.py"]
