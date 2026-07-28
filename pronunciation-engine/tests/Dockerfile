# Image chạy test — engine + pytest.
#
# Tách khỏi image phục vụ để production không mang theo pytest. Cần `phonara-engine` làm
# nền vì `test_diagnosis.py` dựng `torch.Tensor` trực tiếp: quy tắc hợp nhất nhận log-probs
# nên không test được nếu thiếu torch.
#
# Ba test cũ (alignment, aggregate, calibrate) vẫn thuần logic và chạy được ở image nhẹ;
# image này chạy được cả bốn.
FROM phonara-engine
RUN pip install --no-cache-dir pytest==8.3.4
WORKDIR /w
