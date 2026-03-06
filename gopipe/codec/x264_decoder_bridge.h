#include <stdint.h>
#include <stdlib.h>
#include <errno.h>
#include <libavcodec/avcodec.h>
#include <libavutil/error.h>
#include <libavutil/frame.h>


#define H264DEC_EAGAIN AVERROR(EAGAIN)

#define ERR_CODEC_NOT_FOUND -1
#define ERR_ALLOC_CONTEXT -2
#define ERR_OPEN_CODEC -3

typedef struct H264Decoder
{
    AVCodecContext *ctx;
    AVFrame *frame;
    AVPacket *pkt;
} H264Decoder;

H264Decoder *h264dec_new(int *rc)
{
    // find codec
    const AVCodec *codec = avcodec_find_decoder(AV_CODEC_ID_H264);
    if (!codec)
    {
        *rc = ERR_CODEC_NOT_FOUND;
        return NULL;
    }

    // allocate our own sturct
    H264Decoder *d = (H264Decoder *)malloc(sizeof(H264Decoder));

    // allocate context
    d->ctx = avcodec_alloc_context3(codec);
    if (!d->ctx)
    {
        *rc = ERR_ALLOC_CONTEXT;
        free(d);
        return NULL;
    }

    // configure context to use codec
    if (avcodec_open2(d->ctx, codec, NULL) < 0)
    {
        *rc = ERR_OPEN_CODEC;
        avcodec_free_context(&d->ctx);
        free(d);
        return NULL;
    }

    d->frame = av_frame_alloc();
    d->pkt = av_packet_alloc();

    *rc = 0;
    return d;
}

int h264dec_decode(H264Decoder *d, const uint8_t *data, int size)
{
    // copy go array to avpacket
    int err = av_new_packet(d->pkt, size);
    if (err < 0)
        return err;
    memcpy(d->pkt->data, data, size);

    // send it to the decoder
    err = avcodec_send_packet(d->ctx, d->pkt);

    av_packet_unref(d->pkt);

    return err;
}

// Returns 0 if a frame was decoded, AVERROR(EAGAIN) if more packets are needed,
// or another negative AVERROR on failure.
int h264dec_get_frame(H264Decoder *d)
{
    return avcodec_receive_frame(d->ctx, d->frame);
}

int h264dec_width(H264Decoder *d)
{
    return d->frame->width;
}

int h264dec_height(H264Decoder *d)
{
    return d->frame->height;
}

uint8_t *h264dec_y_plane(H264Decoder *d)
{
    return d->frame->data[0];
}

uint8_t *h264dec_u_plane(H264Decoder *d)
{
    return d->frame->data[1];
}

uint8_t *h264dec_v_plane(H264Decoder *d)
{
    return d->frame->data[2];
}

int h264dec_y_linesize(H264Decoder *d)
{
    return d->frame->linesize[0];
}

int h264dec_u_linesize(H264Decoder *d)
{
    return d->frame->linesize[1];
}

int h264dec_v_linesize(H264Decoder *d)
{
    return d->frame->linesize[2];
}

void h264dec_free(H264Decoder *d)
{
    if (!d) return;
    av_frame_free(&d->frame);
    av_packet_free(&d->pkt);
    avcodec_free_context(&d->ctx);
    free(d);
}
