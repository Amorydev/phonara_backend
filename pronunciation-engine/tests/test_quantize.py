"""Cờ PE_QUANTIZE và ba lớp bảo vệ quanh nó.

Test được không cần model thật: `quantize_dynamic` chỉ quan tâm tới các `nn.Linear` trong
cây module, nên một module hai lớp Linear chứng minh đúng thứ cần chứng minh — và chạy
trong vài mili giây thay vì nạp 1,27 GB weights.
"""

from __future__ import annotations

from types import SimpleNamespace

import pytest
import torch
from torch import nn

from app.config import Settings
from app.engine.loader import _quantize_int8


class _TinyModel(nn.Module):
    """Đủ để `quantize_dynamic` có việc làm: Linear bị đổi, Conv1d thì không."""

    def __init__(self) -> None:
        super().__init__()
        self.conv = nn.Conv1d(4, 4, kernel_size=3, padding=1)
        self.fc1 = nn.Linear(8, 16)
        self.fc2 = nn.Linear(16, 4)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        return self.fc2(self.fc1(x))


def _fake_quant_backend(monkeypatch, engines: list[str]) -> None:
    """Giả lập danh sách backend lượng tử hoá.

    `torch.backends.quantized.supported_engines` là property CHỈ ĐỌC — gán thẳng vào nó ném
    `RuntimeError: Assignment not supported`. Thay cả module con bằng một namespace là cách
    duy nhất kiểm được nhánh "máy này không có fbgemm" trên một máy có fbgemm (và ngược lại).
    """
    monkeypatch.setattr(
        torch.backends,
        "quantized",
        SimpleNamespace(supported_engines=engines, engine=engines[0]),
    )


needs_fbgemm = pytest.mark.skipif(
    "fbgemm" not in torch.backends.quantized.supported_engines,
    reason="fbgemm chỉ có trên x86 — hành vi trên máy ARM không suy ra được cho VPS",
)


# ── giá trị cấu hình ────────────────────────────────────────────────────────────


def test_default_is_off():
    # Mặc định phải là TẮT: bật lượng tử hoá đổi điểm số, và không ai nên nhận thay đổi đó
    # mà không tự tay yêu cầu.
    assert Settings().resolved_quantize() == "off"


def test_int8_is_accepted():
    assert Settings(quantize="int8").resolved_quantize() == "int8"


def test_typo_raises_instead_of_falling_back():
    """Test quan trọng nhất về cấu hình.

    Rơi về `off` khi gõ sai nghĩa là bạn tưởng đang chạy INT8 trong khi thực tế là FP32,
    rồi đi tìm nguyên nhân "vì sao không nhanh lên" ở nhầm chỗ.
    """
    with pytest.raises(ValueError, match="không hợp lệ"):
        Settings(quantize="INT8").resolved_quantize()
    with pytest.raises(ValueError, match="không hợp lệ"):
        Settings(quantize="true").resolved_quantize()


# ── ba lớp bảo vệ trong _quantize_int8 ──────────────────────────────────────────


def test_refuses_non_cpu_device():
    # INT8 động chạy bằng kernel CPU. Bật cùng CUDA là âm thầm bỏ GPU đã trả tiền.
    with pytest.raises(RuntimeError, match="chỉ dùng được với CPU"):
        _quantize_int8(_TinyModel(), "cuda")


def test_refuses_when_fbgemm_missing(monkeypatch):
    """Không có fbgemm thì INT8 đo được 0,94× — CHẬM HƠN FP32 mà vẫn lệch điểm.

    Khởi động được trong tình trạng đó là kịch bản tệ nhất: mất chính xác, không được gì.
    """
    _fake_quant_backend(monkeypatch, ["none", "qnnpack"])
    with pytest.raises(RuntimeError, match="fbgemm"):
        _quantize_int8(_TinyModel(), "cpu")


@needs_fbgemm
def test_quantizes_in_place():
    """Test quan trọng nhất trong file — đây là lớp bảo vệ chống OOM.

    `quantize_dynamic` mặc định deepcopy cả model trước khi đổi. Với weights FP32 ~1,27 GB
    thì bản sao đẩy đỉnh bộ nhớ lên ~2,5 GB, vượt hạn mức 2G của container engine trong
    `docker-compose.prod.yml` — container chết ngay lúc khởi động, và triệu chứng (OOMKill)
    không hề chỉ về nguyên nhân (thiếu `inplace=True`).

    Cùng một đối tượng trả về ⇔ không có bản sao nào được tạo.
    """
    model = _TinyModel().eval()
    assert _quantize_int8(model, "cpu") is model


@needs_fbgemm
def test_only_linear_layers_change():
    model = _TinyModel().eval()
    quantized = _quantize_int8(model, "cpu")

    assert isinstance(quantized.fc1, nn.quantized.dynamic.Linear)
    assert isinstance(quantized.fc2, nn.quantized.dynamic.Linear)
    # Bộ trích đặc trưng tích chập của wav2vec2 KHÔNG được lượng tử hoá — phần tăng tốc chỉ
    # đến từ khối transformer, và ước lượng tốc độ nào bỏ qua điều này sẽ quá lạc quan.
    assert isinstance(quantized.conv, nn.Conv1d)


@needs_fbgemm
def test_quantized_model_still_runs():
    model = _TinyModel().eval()
    quantized = _quantize_int8(model, "cpu")

    with torch.no_grad():
        assert quantized(torch.zeros(1, 8)).shape == (1, 4)
