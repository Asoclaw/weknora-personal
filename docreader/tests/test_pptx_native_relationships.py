import io
import unittest
import zipfile
from docreader.parser.pptx_media import list_pptx_media

class NativePPTMediaTests(unittest.TestCase):
    def package(self, external=False):
        stream=io.BytesIO()
        with zipfile.ZipFile(stream,'w') as z:
            for page,target in [(1,'media/a.png'),(2,'../media/b.png'),(10,'media/a.png')]:
                z.writestr(f'ppt/slides/slide{page}.xml', '<p:sld xmlns:p="p" xmlns:a="a" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><a:blip r:embed="rId1"/></p:sld>')
                mode='External' if external else 'Internal'
                z.writestr(f'ppt/slides/_rels/slide{page}.xml.rels',f'<Relationships><Relationship Id="rId1" Type="office/image" Target="{target}" TargetMode="{mode}"/></Relationships>')
            z.writestr('ppt/slides/media/a.png',b'first')
            z.writestr('ppt/media/b.png',b'second')
        return stream.getvalue()

    def test_relationships_preserve_page_order_and_shared_images(self):
        images=list_pptx_media(self.package())
        self.assertEqual([data for _,data in images],[b'first',b'second',b'first'])
        self.assertEqual([name for name,_ in images],['ppt/slides/media/a.png','ppt/media/b.png','ppt/slides/media/a.png'])

    def test_no_relationship_legacy_media_fallback(self):
        stream=io.BytesIO()
        with zipfile.ZipFile(stream,'w') as z:z.writestr('ppt/media/old.png',b'legacy')
        self.assertEqual(list_pptx_media(stream.getvalue()),[('ppt/media/old.png',b'legacy')])

if __name__=='__main__':unittest.main()
