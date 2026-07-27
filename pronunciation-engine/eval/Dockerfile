# Image đánh giá — engine + công cụ phân tích. Tách khỏi image phục vụ để image
# production không mang theo pyarrow (~50 MB) và dữ liệu đánh giá.
FROM phonara-engine
RUN pip install --no-cache-dir pyarrow==18.1.0
WORKDIR /w
